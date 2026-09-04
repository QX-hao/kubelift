package kubeadm

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	bootstrapTokenPattern = regexp.MustCompile(`^[a-z0-9]{6}\.[a-z0-9]{16}$`)
	caCertHashPattern     = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	joinCommandPattern    = regexp.MustCompile(`^kubeadm join ([^ ]+) --token ([a-z0-9]{6}\.[a-z0-9]{16}) --discovery-token-ca-cert-hash (sha256:[a-f0-9]{64})$`)
	certificateKeyPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// JoinCredentials 是 kubeadm 为新节点签发的短期发现凭据。
type JoinCredentials struct {
	APIServerEndpoint string
	Token             string
	CACertHash        string
}

type joinConfiguration struct {
	APIVersion       string                  `yaml:"apiVersion"`
	Kind             string                  `yaml:"kind"`
	Discovery        joinDiscovery           `yaml:"discovery"`
	NodeRegistration nodeRegistrationOptions `yaml:"nodeRegistration"`
	ControlPlane     *joinControlPlane       `yaml:"controlPlane,omitempty"`
}

type joinControlPlane struct {
	LocalAPIEndpoint apiEndpoint `yaml:"localAPIEndpoint"`
	CertificateKey   string      `yaml:"certificateKey"`
}

type joinDiscovery struct {
	BootstrapToken bootstrapTokenDiscovery `yaml:"bootstrapToken"`
}

type bootstrapTokenDiscovery struct {
	Token             string   `yaml:"token"`
	APIServerEndpoint string   `yaml:"apiServerEndpoint"`
	CACertHashes      []string `yaml:"caCertHashes"`
}

// ParseJoinCommand 只接受 kubeadm --print-join-command 的标准单行输出。
func ParseJoinCommand(output string) (JoinCredentials, error) {
	matches := joinCommandPattern.FindStringSubmatch(strings.TrimSpace(output))
	if len(matches) != 4 {
		return JoinCredentials{}, fmt.Errorf("kubeadm returned an unsupported join command")
	}
	return JoinCredentials{APIServerEndpoint: matches[1], Token: matches[2], CACertHash: matches[3]}, nil
}

// ParseCertificateKey 从 upload-certs 输出中提取唯一的证书解密密钥。
func ParseCertificateKey(output string) (string, error) {
	keys := make([]string, 0, 1)
	for _, line := range strings.Split(output, "\n") {
		candidate := strings.TrimSpace(line)
		if certificateKeyPattern.MatchString(candidate) {
			keys = append(keys, candidate)
		}
	}
	if len(keys) != 1 {
		return "", fmt.Errorf("kubeadm returned %d certificate keys, want exactly one", len(keys))
	}
	return keys[0], nil
}

// GenerateWorkerJoinConfig 生成 Worker 使用的 kubeadm JoinConfiguration。
func GenerateWorkerJoinConfig(credentials JoinCredentials, nodeName string) ([]byte, error) {
	configuration, err := newJoinConfiguration(credentials, nodeName)
	if err != nil {
		return nil, err
	}
	return marshalJoinConfiguration(configuration)
}

// GenerateControlPlaneJoinConfig 生成新增 Master 使用的 kubeadm JoinConfiguration。
func GenerateControlPlaneJoinConfig(credentials JoinCredentials, nodeName, advertiseAddress, certificateKey string) ([]byte, error) {
	configuration, err := newJoinConfiguration(credentials, nodeName)
	if err != nil {
		return nil, err
	}
	address, err := netip.ParseAddr(advertiseAddress)
	if err != nil || !address.Is4() || !address.IsGlobalUnicast() {
		return nil, fmt.Errorf("control-plane advertise address must be a usable IPv4 address")
	}
	if !certificateKeyPattern.MatchString(certificateKey) {
		return nil, fmt.Errorf("control-plane certificate key has an invalid format")
	}
	configuration.ControlPlane = &joinControlPlane{
		LocalAPIEndpoint: apiEndpoint{AdvertiseAddress: address.String(), BindPort: 6443},
		CertificateKey:   certificateKey,
	}
	return marshalJoinConfiguration(configuration)
}

func newJoinConfiguration(credentials JoinCredentials, nodeName string) (joinConfiguration, error) {
	if strings.TrimSpace(credentials.APIServerEndpoint) == "" {
		return joinConfiguration{}, fmt.Errorf("join API server endpoint is required")
	}
	if !bootstrapTokenPattern.MatchString(credentials.Token) {
		return joinConfiguration{}, fmt.Errorf("join bootstrap token has an invalid format")
	}
	if !caCertHashPattern.MatchString(credentials.CACertHash) {
		return joinConfiguration{}, fmt.Errorf("join CA certificate hash has an invalid format")
	}
	if strings.TrimSpace(nodeName) == "" {
		return joinConfiguration{}, fmt.Errorf("join node name is required")
	}
	return joinConfiguration{
		APIVersion: kubeadmAPIVersion,
		Kind:       "JoinConfiguration",
		Discovery: joinDiscovery{BootstrapToken: bootstrapTokenDiscovery{
			Token: credentials.Token, APIServerEndpoint: credentials.APIServerEndpoint,
			CACertHashes: []string{credentials.CACertHash},
		}},
		NodeRegistration: nodeRegistrationOptions{
			Name: nodeName, CRISocket: criSocket, ImagePullPolicy: "Never",
		},
	}, nil
}

func marshalJoinConfiguration(configuration joinConfiguration) ([]byte, error) {
	contents, err := yaml.Marshal(configuration)
	if err != nil {
		return nil, fmt.Errorf("encode kubeadm worker join configuration: %w", err)
	}
	return contents, nil
}
