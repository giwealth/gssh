package gssh

import (
	"bytes"
	"errors"
	"path/filepath"
	"io/ioutil"
	"net"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Server ssh配置信息
type Server struct {
	Options      ServerOptions
	ProxyOptions ServerOptions
}

// ServerOptions ssh配置
type ServerOptions struct {
	Addr       string
	Port       string
	User       string
	Key        string
	KeyFile    string
	Password   string
	SocketFile string
	Timeout    time.Duration
}

func getPrivateKeyFile(file string) (ssh.Signer, error) {
	buf, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, err
	}

	return ssh.ParsePrivateKey(buf)
}

// ToSSHClientConfig 转换成SSHClientConfig
func (opt ServerOptions) ToSSHClientConfig() (*ssh.ClientConfig, error) {
	auths := []ssh.AuthMethod{}
	if opt.Password != "" {
		auths = append(auths, ssh.Password(opt.Password))
	}

	if opt.KeyFile != "" {
		pubkey, err := getPrivateKeyFile(opt.KeyFile)
		if err != nil {
			return nil, err
		}
		auths = append(auths, ssh.PublicKeys(pubkey))
	}

	if opt.Key != "" {
		signer, err := ssh.ParsePrivateKey([]byte(opt.Key))
		if err != nil {
			return nil, err
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}

	if opt.SocketFile != "" {
		sshAgent, err := net.Dial("unix", opt.SocketFile)
		if err != nil {
			return nil, err
		}
		// TODO Close socket
		auths = append(auths, ssh.PublicKeysCallback(agent.NewClient(sshAgent).Signers))
	}

	sshConfig := &ssh.ClientConfig{
		Timeout:         opt.Timeout,
		User:            opt.User,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	return sshConfig, nil
}

// dial 连接到远程服务器并返回*ssh.Session
func (s *Server) dial() (*ssh.Client, error) {
	// 开启ssh代理
	if s.ProxyOptions.Addr != "" {
		return s.dialProxyServer()
	}

	return s.dialServer()
}

func (s *Server) dialServer() (*ssh.Client, error) {
	sshClientConfig, err := s.Options.ToSSHClientConfig()
	if err != nil {
		return nil, err
	}

	addr := net.JoinHostPort(s.Options.Addr, s.Options.Port)
	return ssh.Dial("tcp", addr, sshClientConfig)
}

func (s *Server) dialProxyServer() (*ssh.Client, error) {
	if s.ProxyOptions.Addr == "" {
		return nil, errors.New("proxy server address is empty")
	}

	proxySSHClientConfig, err := s.ProxyOptions.ToSSHClientConfig()
	if err != nil {
		return nil, err
	}

	proxyAddr := net.JoinHostPort(s.ProxyOptions.Addr, s.ProxyOptions.Port)
	proxyClient, err := ssh.Dial("tcp", proxyAddr, proxySSHClientConfig)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			// 返回的error如果没有被使用
			// 有些代码检测工具会报错
			_ = proxyClient.Close()
		}
	}()

	addr := net.JoinHostPort(s.Options.Addr, s.Options.Port)
	conn, err := proxyClient.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = conn.Close()
		}
	}()

	// ncc, chans, reqs, err := ssh.NewClientConn(conn, addr, proxySSHClientConfig)
	targetSSHClientConfig, err := s.Options.ToSSHClientConfig()
	if err != nil {
		return nil, err
	}
	ncc, chans, reqs, err := ssh.NewClientConn(conn, addr, targetSSHClientConfig)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = ncc.Close()
		}
	}()

	client := ssh.NewClient(ncc, chans, reqs)

	return client, nil
}

// Command 执行命令
func (s *Server) Command(command string) (string, error) {
	client, err := s.dial()
	if err != nil {
		return "", err
	}
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	// 创建伪终端, 执行sudo命令时需设置
	modes := ssh.TerminalModes{
		ssh.ECHO:          53,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}

	err = session.RequestPty("xterm", 80, 40, modes)
	if err != nil {
		return "", err
	}

	var stdOutBuf bytes.Buffer
	session.Stdout = &stdOutBuf

	err = session.Run(command)
	if err != nil {
		return "", err
	}

	// 去掉输出结果中末尾换行符
	stdout := strings.TrimSuffix(string(stdOutBuf.Bytes()), "\n")

	// 如果执行ls命令去掉结果中的多余空格,并返回以换行为分割符的字符串
	if re, _ := regexp.MatchString(".*ls.*", command); re {
		c := strings.Replace(command, ".*ls", "ls", -1)
		if re, _ := regexp.MatchString("ls -l.*", c); !re {
			stdout = strings.TrimSuffix(replaceSpace(stdout), " ")
		}
	}
	return stdout, err
}

// 替换字符串中连续多个空格为一个
func replaceSpace(str string) string {
	if str == "" {
		return ""
	}
	reg := regexp.MustCompile("\\s+")
	return reg.ReplaceAllString(str, "\n")
}

// Get 使用sftp从远程服务器下载文件
func (s *Server) Get(src, dst string) (err error) {
	sshClient, err := s.dial()
	if err != nil {
		return err
	}
	defer sshClient.Close()

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		return err
	}
	defer sftpClient.Close()

	// 返回匹配的所有名称
	m, err := sftpClient.Glob(strings.TrimSuffix(src, "/"))
	if err != nil {
		return err
	}

	if len(m) == 0 {
		return errors.New("file or directory does not exist")
	}

	for _, l := range m {
		f, err := sftpClient.Stat(l)
		if err != nil {
			return err
		}
	
		// src 是目录
		if f.IsDir() {
			w := sftpClient.Walk(l)
			for w.Step() {
				if w.Err() != nil {
					continue
				}
	
				// 获取src下的文件和目录
				remotePath := w.Path()

				// 获取以src为root的相对目录
				p := strings.TrimPrefix(remotePath, path.Dir(strings.TrimSuffix(l, "/")))
	
				f := w.Stat()
				if f.IsDir() {
					path := path.Join(dst, p)
					os.MkdirAll(path, 0755)
				} else {
					err := getFileBySFTP(remotePath, path.Join(dst, p), sftpClient)
					if err != nil {
						return err
					}
				}
			}
		} else {
			err := getFileBySFTP(l, path.Join(dst, filepath.Base(l)), sftpClient)
			if err != nil {
				return nil
			}
		}
	}

	return nil
}

func getFileBySFTP(src, dst string, sftpClient *sftp.Client) error {
	srcFile, err := sftpClient.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err = srcFile.WriteTo(dstFile); err != nil {
		return err
	}

	return nil
}

// Put 使用sftp从本地上传文件到远程服务器
func (s *Server) Put(src, dst string) (err error) {
	sshClient, err := s.dial()
	if err != nil {
		return err
	}
	defer sshClient.Close()

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		return err
	}
	defer sftpClient.Close()

	m, err := filepath.Glob(strings.TrimSuffix(src, "/"))
	if err != nil {
		return err
	}

	if len(m) == 0 {
		return errors.New("file or directory does not exist")
	}

	// m 本地源文件或目录
	for _, l := range m {
		srcFile, err := os.Open(l)
		if err != nil {
			return err
		}
		defer srcFile.Close()
	
		srcInfo, err := srcFile.Stat()
		if err != nil {
			return err
		}
	
		if srcInfo.IsDir() {
			err := filepath.Walk(l, func(p string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.IsDir() {
					// 获取以src为root的相对目录
					remoteBasePath := strings.TrimPrefix(p, path.Dir(strings.TrimSuffix(l, "/")))
					// 以dst为root创建目录
					err := sftpClient.MkdirAll(path.Join(dst, remoteBasePath))
					if err != nil {
						return err
					}
				} else {
					// 获取以src为root的相对文件路径
					file := strings.TrimPrefix(p, path.Dir(strings.TrimSuffix(l, "/")))
					err := putFileBySFTP(p, path.Join(dst, file), sftpClient)
					if err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
		} else {
			err := putFileBySFTP(l, path.Join(dst, path.Base(l)), sftpClient)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func putFileBySFTP(src, dst string, sftpClient *sftp.Client) error {
	dstFile, err := sftpClient.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	f, err := ioutil.ReadFile(src)
	if err != nil {
		return err
	}

	_, err = dstFile.Write(f)
	if err != nil {
		return err
	}

	return nil
}
