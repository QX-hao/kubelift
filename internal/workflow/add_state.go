package workflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/QX-hao/kubelift/internal/config"
	"gopkg.in/yaml.v3"
)

const (
	addStateAPIVersion = "kubelift.io/v1alpha1"
	addStateKind       = "AddState"
)

type AddPhase string

const (
	AddPhaseStaged         AddPhase = "staged"
	AddPhasePrepared       AddPhase = "prepared"
	AddPhaseImagesImported AddPhase = "images-imported"
	AddPhaseJoinStarting   AddPhase = "join-starting"
	AddPhaseJoined         AddPhase = "joined"
	AddPhaseReady          AddPhase = "ready"
	AddPhaseComplete       AddPhase = "complete"
)

var addPhaseOrder = map[AddPhase]int{
	AddPhaseStaged: 1, AddPhasePrepared: 2, AddPhaseImagesImported: 3,
	AddPhaseJoinStarting: 4, AddPhaseJoined: 5, AddPhaseReady: 6, AddPhaseComplete: 7,
}

// JoinOptions 控制首次加入或从已记录阶段继续执行。
type JoinOptions struct {
	Resume bool
}

type addState struct {
	APIVersion          string   `yaml:"apiVersion"`
	Kind                string   `yaml:"kind"`
	Cluster             string   `yaml:"cluster"`
	KubernetesVersion   string   `yaml:"kubernetesVersion"`
	Role                Role     `yaml:"role"`
	Address             string   `yaml:"address"`
	NodeName            string   `yaml:"nodeName"`
	ConfigurationSHA256 string   `yaml:"configurationSHA256"`
	BundleSHA256        string   `yaml:"bundleSHA256"`
	Phase               AddPhase `yaml:"phase"`
}

func (s addState) reached(phase AddPhase) bool { return addPhaseOrder[s.Phase] >= addPhaseOrder[phase] }

func prepareAddState(configuration config.Config, target Target, nodeName, root string, resume bool) (addState, string, error) {
	configHash, err := configurationHash(configuration)
	if err != nil {
		return addState{}, "", err
	}
	bundleHash, err := fileSHA256(configuration.Spec.Offline.Bundle)
	if err != nil {
		return addState{}, "", err
	}
	statePath := addStatePath(root, configuration.Metadata.Name, target.Role, target.Address)
	state, err := loadAddState(statePath)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return addState{}, "", fmt.Errorf("load add state %q: %w", statePath, err)
	}
	if !resume {
		if exists {
			return addState{}, "", fmt.Errorf("add state already exists at %q; use --resume to continue", statePath)
		}
		return addState{
			APIVersion: addStateAPIVersion, Kind: addStateKind,
			Cluster: configuration.Metadata.Name, KubernetesVersion: configuration.Spec.Kubernetes.Version,
			Role: target.Role, Address: target.Address, NodeName: nodeName,
			ConfigurationSHA256: configHash, BundleSHA256: bundleHash,
		}, statePath, nil
	}
	if !exists {
		return addState{}, "", fmt.Errorf("no add state exists at %q; cannot resume", statePath)
	}
	if state.Cluster != configuration.Metadata.Name || state.KubernetesVersion != configuration.Spec.Kubernetes.Version ||
		state.Role != target.Role || state.Address != target.Address || state.NodeName != nodeName ||
		state.ConfigurationSHA256 != configHash || state.BundleSHA256 != bundleHash {
		return addState{}, "", fmt.Errorf("add state does not match the current target, cluster configuration, and offline bundle")
	}
	return state, statePath, nil
}

func loadAddState(path string) (addState, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return addState{}, err
	}
	var state addState
	if err := yaml.Unmarshal(contents, &state); err != nil {
		return addState{}, fmt.Errorf("decode add state: %w", err)
	}
	if state.APIVersion != addStateAPIVersion || state.Kind != addStateKind {
		return addState{}, fmt.Errorf("unsupported add state %q %q", state.APIVersion, state.Kind)
	}
	if _, exists := addPhaseOrder[state.Phase]; !exists {
		return addState{}, fmt.Errorf("unsupported add phase %q", state.Phase)
	}
	return state, nil
}

func saveAddState(path string, state addState, phase AddPhase) error {
	state.Phase = phase
	contents, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode add state: %w", err)
	}
	if err := writePrivateFile(path, contents); err != nil {
		return fmt.Errorf("save add state: %w", err)
	}
	return nil
}

func addStatePath(root, cluster string, role Role, address string) string {
	safeAddress := strings.NewReplacer(".", "-", ":", "-").Replace(address)
	return filepath.Join(root, fmt.Sprintf("%s-add-%s-%s.yaml", cluster, role, safeAddress))
}
