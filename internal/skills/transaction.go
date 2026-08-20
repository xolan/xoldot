package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type stagedSkill struct {
	root          string
	canonical     string
	compatibility string
	digest        string
}

func (manager Manager) stageSkill(name, source string) (stagedSkill, error) {
	root, err := os.MkdirTemp(filepath.Dir(manager.ManagedHome), ".xoldot-skill-stage-*")
	if err != nil {
		return stagedSkill{}, fmt.Errorf("create skill staging directory: %w", err)
	}
	candidate := stagedSkill{
		root:          root,
		canonical:     filepath.Join(root, "home", ".agents", "skills", name),
		compatibility: filepath.Join(root, "home", ".claude", "skills", name),
	}
	managedHome := filepath.Join(root, "home")
	if err := os.MkdirAll(managedHome, 0o755); err != nil {
		candidate.cleanup()
		return stagedSkill{}, fmt.Errorf("create staged managed home: %w", err)
	}
	if err := manager.runAdd(name, source, managedHome); err != nil {
		candidate.cleanup()
		return stagedSkill{}, err
	}
	candidate.digest, err = digestSkill(candidate.canonical)
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

type skillTransaction struct {
	canonical          string
	compatibility      string
	backupDirectory    string
	hadCanonical       bool
	hadCompatibility   bool
	installedCanonical bool
	installedMirror    bool
}

func beginSkillTransaction(canonical, compatibility, managedHome string, allowExisting bool) (*skillTransaction, error) {
	directory, err := os.MkdirTemp(filepath.Dir(managedHome), ".xoldot-skill-backup-*")
	if err != nil {
		return nil, fmt.Errorf("create skill backup directory: %w", err)
	}
	transaction := &skillTransaction{
		canonical:       canonical,
		compatibility:   compatibility,
		backupDirectory: directory,
	}
	if !allowExisting {
		for _, path := range []string{canonical, compatibility} {
			if _, err := os.Lstat(path); err == nil {
				_ = os.RemoveAll(directory)
				return nil, fmt.Errorf("managed skill path %s appeared while installing; refusing to overwrite it", path)
			} else if !errors.Is(err, os.ErrNotExist) {
				_ = os.RemoveAll(directory)
				return nil, fmt.Errorf("inspect managed skill path %s: %w", path, err)
			}
		}
	}
	transaction.hadCanonical, err = transaction.stage(canonical, "canonical")
	if err != nil {
		_ = transaction.rollback()
		return nil, err
	}
	transaction.hadCompatibility, err = transaction.stage(compatibility, "compatibility")
	if err != nil {
		_ = transaction.rollback()
		return nil, err
	}
	return transaction, nil
}

func replaceSkill(candidate stagedSkill, canonical, compatibility, managedHome string, allowExisting bool, save func() error) error {
	defer candidate.cleanup()
	transaction, err := beginSkillTransaction(canonical, compatibility, managedHome, allowExisting)
	if err != nil {
		return err
	}
	if err := transaction.install(candidate); err != nil {
		return errors.Join(err, transaction.rollback())
	}
	if err := save(); err != nil {
		return errors.Join(err, transaction.rollback())
	}
	return transaction.commit()
}

func removeSkill(canonical, compatibility, managedHome string, save func() error) error {
	transaction, err := beginSkillTransaction(canonical, compatibility, managedHome, true)
	if err != nil {
		return err
	}
	if err := save(); err != nil {
		return errors.Join(err, transaction.rollback())
	}
	return transaction.commit()
}

func (transaction *skillTransaction) stage(path, name string) (bool, error) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("inspect managed skill path %s: %w", path, err)
	}
	if err := os.Rename(path, filepath.Join(transaction.backupDirectory, name)); err != nil {
		return false, fmt.Errorf("stage managed skill path %s: %w", path, err)
	}
	return true, nil
}

func (transaction *skillTransaction) install(candidate stagedSkill) error {
	if err := os.MkdirAll(filepath.Dir(transaction.canonical), 0o755); err != nil {
		return err
	}
	if err := os.Rename(candidate.canonical, transaction.canonical); err != nil {
		return fmt.Errorf("install canonical skill: %w", err)
	}
	transaction.installedCanonical = true
	if err := os.MkdirAll(filepath.Dir(transaction.compatibility), 0o755); err != nil {
		return err
	}
	if err := os.Rename(candidate.compatibility, transaction.compatibility); err != nil {
		return fmt.Errorf("install Claude compatibility links: %w", err)
	}
	transaction.installedMirror = true
	return nil
}

func (transaction *skillTransaction) rollback() error {
	var rollbackErrors []error
	if transaction.installedMirror {
		if err := os.RemoveAll(transaction.compatibility); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	if transaction.installedCanonical {
		if err := os.RemoveAll(transaction.canonical); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	if transaction.hadCanonical {
		if err := restorePath(filepath.Join(transaction.backupDirectory, "canonical"), transaction.canonical); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	if transaction.hadCompatibility {
		if err := restorePath(filepath.Join(transaction.backupDirectory, "compatibility"), transaction.compatibility); err != nil {
			rollbackErrors = append(rollbackErrors, err)
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
