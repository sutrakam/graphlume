package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// declinedPath is where a declined setup offer is remembered in the user
// config: accuracy.indexers.declined, a list of offer keys.
var declinedPath = []string{"accuracy", "indexers", "declined"}

// RememberDeclined adds key to accuracy.indexers.declined in the user config
// at path, creating the file and the keys when they do not exist.
//
// The file is edited as a YAML node tree rather than re-encoded from
// UserConfig, so the user's comments, key order and any key this build does
// not know survive. Only the user config is ever written: a declined offer is
// a decision about this machine, and the repository has no say in it.
func RememberDeclined(path, key string) error {
	doc, err := readUserDoc(path)
	if err != nil {
		return err
	}
	seq := ensureSeq(doc, declinedPath)
	for _, n := range seq.Content {
		if n.Value == key {
			return nil
		}
	}
	seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key})
	return writeUserDoc(path, doc)
}

// ForgetDeclined empties accuracy.indexers.declined in the user config at
// path and returns how many answers it held. A missing file or key is zero,
// not an error.
func ForgetDeclined(path string) (int, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	doc, err := readUserDoc(path)
	if err != nil {
		return 0, err
	}
	parent := lookupMap(doc, declinedPath[:len(declinedPath)-1])
	if parent == nil {
		return 0, nil
	}
	last := declinedPath[len(declinedPath)-1]
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value != last {
			continue
		}
		n := len(parent.Content[i+1].Content)
		parent.Content = append(parent.Content[:i], parent.Content[i+2:]...)
		return n, writeUserDoc(path, doc)
	}
	return 0, nil
}

// readUserDoc parses the user config into a document whose root is a mapping.
// A missing or empty file is an empty mapping.
func readUserDoc(path string) (*yaml.Node, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path is UserConfigPath(), never repository-derived
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read user config %s: %w", path, err)
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(b)) > 0 {
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("parse user config %s: %w", path, friendlyYAMLError(err, b))
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode}
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("user config %s: top level is not a mapping", path)
	}
	return &doc, nil
}

func writeUserDoc(path string, doc *yaml.Node) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("encode user config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("encode user config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write user config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write user config: %w", err)
	}
	return nil
}

// lookupMap walks mapping keys from the document root and returns the mapping
// at keys, or nil when any of them is absent or not a mapping.
func lookupMap(doc *yaml.Node, keys []string) *yaml.Node {
	cur := doc.Content[0]
	for _, k := range keys {
		next := mapValue(cur, k)
		if next == nil || next.Kind != yaml.MappingNode {
			return nil
		}
		cur = next
	}
	return cur
}

// ensureSeq returns the sequence at keys, creating mappings on the way and
// replacing a non-sequence leaf (such as an empty `declined:`).
func ensureSeq(doc *yaml.Node, keys []string) *yaml.Node {
	cur := doc.Content[0]
	for i, k := range keys {
		last := i == len(keys)-1
		next := mapValue(cur, k)
		want := yaml.MappingNode
		tag := "!!map"
		if last {
			want, tag = yaml.SequenceNode, "!!seq"
		}
		if next == nil {
			next = &yaml.Node{Kind: want, Tag: tag}
			cur.Content = append(cur.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, next)
		} else if next.Kind != want {
			*next = yaml.Node{Kind: want, Tag: tag}
		}
		cur = next
	}
	return cur
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}
