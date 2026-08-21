package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type stagedSkill struct {
	root          string
	canonical     string
	compatibility string
	agents        map[string]struct{}
	agentNames    []string
	digest        string
}

func (manager Manager) stageSkill(name, source string) (stagedSkill, error) {
	root, err := os.MkdirTemp(filepath.Dir(manager.ManagedHome), ".xoldot-skill-stage-*")
	if err != nil {
		return stagedSkill{}, fmt.Errorf("create skill staging directory: %w", err)
	}
	managedHome := filepath.Join(root, "home")
	candidate := stagedSkill{
		root:          root,
		canonical:     canonicalSkillPath(managedHome, name),
		compatibility: compatibilitySkillPath(managedHome, name),
		agents:        make(map[string]struct{}),
	}
	if err := os.MkdirAll(managedHome, 0o755); err != nil {
		candidate.cleanup()
		return stagedSkill{}, fmt.Errorf("create staged managed home: %w", err)
	}
	sourceRoot, err := manager.agentSourceRoot(candidate.root, name, source)
	if err != nil {
		candidate.cleanup()
		return stagedSkill{}, err
	}
	installSource := source
	if sourceRoot != "" {
		installSource = sourceRoot
	}
	if err := manager.runAdd(name, installSource, managedHome); err != nil {
		candidate.cleanup()
		return stagedSkill{}, err
	}
	if err := manager.stageAgents(&candidate, name, sourceRoot); err != nil {
		candidate.cleanup()
		return stagedSkill{}, err
	}
	for name := range candidate.agents {
		candidate.agentNames = append(candidate.agentNames, name)
	}
	sort.Strings(candidate.agentNames)
	candidate.digest, err = digestSkillWithAgents(candidate.canonical, managedHome, candidate.agentNames)
	if err != nil {
		candidate.cleanup()
		return stagedSkill{}, err
	}
	if err := buildCompatibilityMirror(candidate.canonical, candidate.compatibility); err != nil {
		candidate.cleanup()
		return stagedSkill{}, err
	}
	return candidate, nil
}

func (candidate stagedSkill) cleanup() {
	_ = os.RemoveAll(candidate.root)
}

type transactionPath struct {
	live      string
	candidate string
	backup    string
	hadLive   bool
	installed bool
}

type skillTransaction struct {
	backupDirectory string
	paths           []transactionPath
}

func beginSkillTransaction(paths []transactionPath, managedHome string, allowExisting bool) (*skillTransaction, error) {
	directory, err := os.MkdirTemp(filepath.Dir(managedHome), ".xoldot-skill-backup-*")
	if err != nil {
		return nil, fmt.Errorf("create skill backup directory: %w", err)
	}
	transaction := &skillTransaction{backupDirectory: directory, paths: paths}
	for index := range transaction.paths {
		path := &transaction.paths[index]
		path.backup = filepath.Join(directory, fmt.Sprintf("path-%d", index))
		if _, err := os.Lstat(path.live); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			_ = transaction.rollback()
			return nil, fmt.Errorf("inspect managed skill path %s: %w", path.live, err)
		}
		if !allowExisting {
			_ = transaction.rollback()
			return nil, fmt.Errorf("managed skill path %s appeared while installing; refusing to overwrite it", path.live)
		}
		if err := os.Rename(path.live, path.backup); err != nil {
			_ = transaction.rollback()
			return nil, fmt.Errorf("stage managed skill path %s: %w", path.live, err)
		}
		path.hadLive = true
	}
	return transaction, nil
}

func replaceSkill(candidate stagedSkill, canonical, compatibility, managedHome string, previousAgents []string, allowExisting bool, save func() error) error {
	defer candidate.cleanup()
	paths := replacementPaths(candidate, canonical, compatibility, managedHome, previousAgents)
	transaction, err := beginSkillTransaction(paths, managedHome, allowExisting)
	if err != nil {
		return err
	}
	if err := transaction.install(); err != nil {
		return errors.Join(err, transaction.rollback())
	}
	if err := save(); err != nil {
		return errors.Join(err, transaction.rollback())
	}
	return transaction.commit()
}

func removeSkill(canonical, compatibility, managedHome string, agents []string, save func() error) error {
	paths := []transactionPath{{live: canonical}, {live: compatibility}}
	for _, relative := range agents {
		paths = append(paths,
			transactionPath{live: canonicalAgentPath(managedHome, relative)},
			transactionPath{live: claudeAgentPath(managedHome, relative)},
		)
	}
	transaction, err := beginSkillTransaction(paths, managedHome, true)
	if err != nil {
		return err
	}
	if err := save(); err != nil {
		return errors.Join(err, transaction.rollback())
	}
	return transaction.commit()
}

func replacementPaths(candidate stagedSkill, canonical, compatibility, managedHome string, previousAgents []string) []transactionPath {
	paths := []transactionPath{
		{live: canonical, candidate: candidate.canonical},
		{live: compatibility, candidate: candidate.compatibility},
	}
	agents := make(map[string]struct{}, len(previousAgents)+len(candidate.agents))
	for _, relative := range previousAgents {
		agents[relative] = struct{}{}
	}
	for relative := range candidate.agents {
		agents[relative] = struct{}{}
	}
	relatives := make([]string, 0, len(agents))
	for relative := range agents {
		relatives = append(relatives, relative)
	}
	sort.Strings(relatives)
	for _, relative := range relatives {
		candidateAgent := ""
		candidateLink := ""
		if _, exists := candidate.agents[relative]; exists {
			candidateAgent = canonicalAgentPath(filepath.Join(candidate.root, "home"), relative)
			candidateLink = claudeAgentPath(filepath.Join(candidate.root, "home"), relative)
		}
		paths = append(paths,
			transactionPath{live: canonicalAgentPath(managedHome, relative), candidate: candidateAgent},
			transactionPath{live: claudeAgentPath(managedHome, relative), candidate: candidateLink},
		)
	}
	return paths
}

func (transaction *skillTransaction) install() error {
	for index := range transaction.paths {
		path := &transaction.paths[index]
		if path.candidate == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path.live), 0o755); err != nil {
			return err
		}
		if err := os.Rename(path.candidate, path.live); err != nil {
			return fmt.Errorf("install managed skill path %s: %w", path.live, err)
		}
		path.installed = true
	}
	return nil
}

func (transaction *skillTransaction) rollback() error {
	var rollbackErrors []error
	for index := len(transaction.paths) - 1; index >= 0; index-- {
		path := transaction.paths[index]
		if path.installed {
			if err := os.RemoveAll(path.live); err != nil {
				rollbackErrors = append(rollbackErrors, err)
			}
		}
		if path.hadLive {
			if err := restorePath(path.backup, path.live); err != nil {
				rollbackErrors = append(rollbackErrors, err)
			}
		}
	}
	if err := os.RemoveAll(transaction.backupDirectory); err != nil {
		rollbackErrors = append(rollbackErrors, err)
	}
	return errors.Join(rollbackErrors...)
}

func restorePath(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	return os.Rename(source, destination)
}

func (transaction *skillTransaction) commit() error {
	if err := os.RemoveAll(transaction.backupDirectory); err != nil {
		return fmt.Errorf("remove previous skill version: %w", err)
	}
	return nil
}
