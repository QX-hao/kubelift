/*
Copyright © 2026 QX-hao

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package remote

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	defaultConnectTimeout = 10 * time.Second
	defaultCommandTimeout = 5 * time.Minute
)

// Target 描述一个需要通过 SSH 管理的远程节点。
type Target struct {
	Address        string
	User           string
	Port           int
	PrivateKeyPath string
	KnownHostsPath string
}

// CommandResult 保存远程命令的输出和退出码。
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Client 是一个已经完成主机指纹校验的 SSH 客户端。
type Client struct {
	client *ssh.Client
}

// Connect 使用指定私钥连接远程节点。
// 只配置公钥认证和 known_hosts 校验，因此不会进入密码或键盘交互。
func Connect(ctx context.Context, target Target) (*Client, error) {
	if err := validateTarget(target); err != nil {
		return nil, err
	}

	privateKey, err := os.ReadFile(target.PrivateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read SSH private key %q: %w", target.PrivateKeyPath, err)
	}
	signer, err := ssh.ParsePrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("parse SSH private key %q: %w", target.PrivateKeyPath, err)
	}

	knownHostsPath := target.KnownHostsPath
	if knownHostsPath == "" {
		knownHostsPath = filepath.Join(filepath.Dir(target.PrivateKeyPath), "known_hosts")
	}
	hostKeyCallback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("load SSH known_hosts %q: %w", knownHostsPath, err)
	}

	address := net.JoinHostPort(target.Address, strconv.Itoa(target.Port))
	connection, err := (&net.Dialer{Timeout: defaultConnectTimeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", address, err)
	}

	// 握手阶段设置硬超时，避免远端服务不响应时永久阻塞。
	_ = connection.SetDeadline(time.Now().Add(defaultConnectTimeout))
	clientConfig := &ssh.ClientConfig{
		User:            target.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCallback,
		Timeout:         defaultConnectTimeout,
	}
	clientConnection, channel, requests, err := ssh.NewClientConn(connection, address, clientConfig)
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("SSH handshake with %s: %w", address, err)
	}
	_ = connection.SetDeadline(time.Time{})
	return &Client{client: ssh.NewClient(clientConnection, channel, requests)}, nil
}

// Run 在远程节点执行一条命令，并在上下文结束时主动关闭连接。
func (c *Client) Run(ctx context.Context, command string) (CommandResult, error) {
	if c == nil || c.client == nil {
		return CommandResult{}, fmt.Errorf("SSH client is not connected")
	}
	if strings.TrimSpace(command) == "" {
		return CommandResult{}, fmt.Errorf("remote command must not be empty")
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultCommandTimeout)
		defer cancel()
	}

	session, err := c.client.NewSession()
	if err != nil {
		return CommandResult{}, fmt.Errorf("create SSH session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	if err := session.Start(command); err != nil {
		return CommandResult{}, fmt.Errorf("start remote command: %w", err)
	}

	wait := make(chan error, 1)
	go func() { wait <- session.Wait() }()
	select {
	case <-ctx.Done():
		_ = c.client.Close()
		return CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}, fmt.Errorf("remote command %q: %w", command, ctx.Err())
	case err := <-wait:
		result := CommandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0}
		if err == nil {
			return result, nil
		}
		if exitError, ok := err.(*ssh.ExitError); ok {
			result.ExitCode = exitError.ExitStatus()
			return result, fmt.Errorf("remote command %q exited with code %d: %w", command, result.ExitCode, err)
		}
		return result, fmt.Errorf("wait for remote command %q: %w", command, err)
	}
}

// UploadFile 通过 SSH 将本地文件传到远程节点。
// 远端先写入临时文件并设置私有权限，完整成功后才替换目标文件。
func (c *Client) UploadFile(ctx context.Context, sourcePath, destinationPath string) error {
	if !filepath.IsAbs(sourcePath) {
		return fmt.Errorf("upload source path must be absolute")
	}
	if !filepath.IsAbs(destinationPath) {
		return fmt.Errorf("upload destination path must be absolute")
	}
	if c == nil || c.client == nil {
		return fmt.Errorf("SSH client is not connected")
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open upload source %q: %w", sourcePath, err)
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return fmt.Errorf("stat upload source %q: %w", sourcePath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("upload source %q must be a regular file", sourcePath)
	}

	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("create SSH upload session: %w", err)
	}
	defer session.Close()

	var stderr bytes.Buffer
	session.Stdin = source
	session.Stderr = &stderr
	partialPath := destinationPath + ".kubelift-partial"
	destinationDirectory := filepath.Dir(destinationPath)
	command := fmt.Sprintf(
		"trap 'rm -f -- %s' EXIT; umask 077 && mkdir -p -- %s && cat > %s && chmod 600 %s && mv -f -- %s %s",
		shellQuote(partialPath),
		shellQuote(destinationDirectory),
		shellQuote(partialPath),
		shellQuote(partialPath),
		shellQuote(partialPath),
		shellQuote(destinationPath),
	)
	if err := session.Start(command); err != nil {
		return fmt.Errorf("start SSH upload: %w", err)
	}

	wait := make(chan error, 1)
	go func() { wait <- session.Wait() }()
	select {
	case <-ctx.Done():
		_ = c.client.Close()
		return fmt.Errorf("upload file %q: %w", sourcePath, ctx.Err())
	case err := <-wait:
		if err == nil {
			return nil
		}
		if output := strings.TrimSpace(stderr.String()); output != "" {
			return fmt.Errorf("upload file %q: %w; remote stderr: %s", sourcePath, err, output)
		}
		return fmt.Errorf("upload file %q: %w", sourcePath, err)
	}
}

// Close 关闭 SSH 连接。
func (c *Client) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}

func validateTarget(target Target) error {
	if strings.TrimSpace(target.Address) == "" {
		return fmt.Errorf("SSH target address is required")
	}
	if strings.TrimSpace(target.User) == "" {
		return fmt.Errorf("SSH user is required")
	}
	if target.Port < 1 || target.Port > 65535 {
		return fmt.Errorf("SSH port must be between 1 and 65535")
	}
	if !filepath.IsAbs(target.PrivateKeyPath) {
		return fmt.Errorf("SSH private key must be an absolute path")
	}
	if target.KnownHostsPath != "" && !filepath.IsAbs(target.KnownHostsPath) {
		return fmt.Errorf("SSH known_hosts path must be absolute")
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
