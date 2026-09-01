package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestConnectAndRun(t *testing.T) {
	clientPublic, clientPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	hostPublic, hostPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	clientSigner, err := ssh.NewPublicKey(clientPublic)
	if err != nil {
		t.Fatalf("marshal client public key: %v", err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPrivate)
	if err != nil {
		t.Fatalf("create host signer: %v", err)
	}

	serverConfig := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) != string(clientSigner.Marshal()) {
				return nil, fmt.Errorf("unexpected client key")
			}
			return nil, nil
		},
	}
	serverConfig.AddHostKey(hostSigner)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen SSH test server: %v", err)
	}
	defer listener.Close()
	uploadData := make(chan []byte, 1)
	go serveTestSSH(t, listener, serverConfig, uploadData)

	privateBlock, err := ssh.MarshalPrivateKey(clientPrivate, "test")
	if err != nil {
		t.Fatalf("marshal client private key: %v", err)
	}
	privatePath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(privatePath, pem.EncodeToMemory(privateBlock), 0o600); err != nil {
		t.Fatalf("write client private key: %v", err)
	}
	host := listener.Addr().(*net.TCPAddr)
	knownHostsPath := filepath.Join(t.TempDir(), "known_hosts")
	hostPublicKey, err := ssh.NewPublicKey(hostPublic)
	if err != nil {
		t.Fatalf("marshal host public key: %v", err)
	}
	knownHostsLine := knownhosts.Line([]string{fmt.Sprintf("[%s]:%d", host.IP, host.Port)}, hostPublicKey)
	if err := os.WriteFile(knownHostsPath, []byte(knownHostsLine+"\n"), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := Connect(ctx, Target{
		Address:        host.IP.String(),
		User:           "root",
		Port:           host.Port,
		PrivateKeyPath: privatePath,
		KnownHostsPath: knownHostsPath,
	})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer client.Close()

	result, err := client.Run(ctx, "hostname && uname -m")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Stdout != "k8s2\nx86_64\n" {
		t.Fatalf("stdout = %q", result.Stdout)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", result.ExitCode)
	}

	sourcePath := filepath.Join(t.TempDir(), "bundle.tar.zst")
	if err := os.WriteFile(sourcePath, []byte("bundle-data"), 0o600); err != nil {
		t.Fatalf("write upload source: %v", err)
	}
	if err := client.UploadFile(ctx, sourcePath, "/tmp/kubelift/bundle.tar.zst"); err != nil {
		t.Fatalf("UploadFile() error = %v", err)
	}
	select {
	case data := <-uploadData:
		if string(data) != "bundle-data" {
			t.Fatalf("uploaded data = %q", data)
		}
	case <-ctx.Done():
		t.Fatalf("wait for uploaded data: %v", ctx.Err())
	}
}

func TestConnectRejectsRelativePrivateKey(t *testing.T) {
	_, err := Connect(context.Background(), Target{
		Address:        "192.168.121.152",
		User:           "root",
		Port:           22,
		PrivateKeyPath: "id_ed25519",
	})
	if err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("Connect() error = %v, want absolute path error", err)
	}
}

func TestRunRejectsDisconnectedClient(t *testing.T) {
	client := &Client{}
	_, err := client.Run(context.Background(), " ")
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("Run() error = %v, want disconnected client error", err)
	}
}

func TestShellQuoteProtectsSingleQuotes(t *testing.T) {
	if got, want := shellQuote("/tmp/a'b"), "'/tmp/a'\"'\"'b'"; got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
}

func TestUploadFileRejectsRelativePaths(t *testing.T) {
	client := &Client{}
	if err := client.UploadFile(context.Background(), "source", "/tmp/destination"); err == nil || !strings.Contains(err.Error(), "source path must be absolute") {
		t.Fatalf("UploadFile() source error = %v", err)
	}
	if err := client.UploadFile(context.Background(), "/tmp/source", "destination"); err == nil || !strings.Contains(err.Error(), "destination path must be absolute") {
		t.Fatalf("UploadFile() destination error = %v", err)
	}
}

func serveTestSSH(t *testing.T, listener net.Listener, config *ssh.ServerConfig, uploadData chan<- []byte) {
	t.Helper()
	connection, err := listener.Accept()
	if err != nil {
		return
	}
	serverConnection, channels, requests, err := ssh.NewServerConn(connection, config)
	if err != nil {
		return
	}
	defer serverConnection.Close()
	go ssh.DiscardRequests(requests)
	for newChannel := range channels {
		channel, channelRequests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer channel.Close()
			for request := range channelRequests {
				if request.Type != "exec" {
					_ = request.Reply(false, nil)
					continue
				}
				var payload struct {
					Command string
				}
				if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
					_ = request.Reply(false, nil)
					return
				}
				_ = request.Reply(true, nil)
				if strings.Contains(payload.Command, "cat >") {
					data, _ := io.ReadAll(channel)
					if uploadData != nil {
						uploadData <- data
					}
					_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct {
						Status uint32 `sshtype:"uint32"`
					}{Status: 0}))
					return
				}
				_, _ = channel.Write([]byte("k8s2\nx86_64\n"))
				_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct {
					Status uint32 `sshtype:"uint32"`
				}{Status: 0}))
				return
			}
		}()
	}
}
