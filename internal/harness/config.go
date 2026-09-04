package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

var installedEvents = []string{"SessionStart", "UserPromptSubmit", "SessionEnd"}

// Installed reports whether every lifecycle event required by the adapter has
// its canonical unfiltered handler group for executable. It is read-only: a
// missing settings file, incomplete installation, stale executable path, or
// filtered/reframed handler returns false without creating or changing files.
func Installed(a Adapter, executable string) (path string, installed bool, err error) {
	path, err = a.SettingsPath()
	if err != nil {
		return "", false, fmt.Errorf("locate %s settings: %w", a.Name(), err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return path, false, fmt.Errorf("resolve executable: %w", err)
	}
	root, _, _, err := readSettingsIfExists(path)
	if err != nil || root == nil {
		return path, false, err
	}
	hooks, err := objectField(root, "hooks", false)
	if err != nil {
		return path, false, fmt.Errorf("%s: %w", path, err)
	}
	if hooks == nil {
		return path, false, nil
	}
	for _, event := range installedEvents {
		groups, err := eventGroups(hooks[event], event, false)
		if err != nil {
			return path, false, fmt.Errorf("%s: %w", path, err)
		}
		handler, err := json.Marshal(a.Handler(executable, event))
		if err != nil {
			return path, false, err
		}
		want, err := json.Marshal(map[string]any{"hooks": []json.RawMessage{handler}})
		if err != nil {
			return path, false, err
		}
		if !hasCanonicalGroup(groups, want) {
			return path, false, nil
		}
	}
	return path, true, nil
}

// Install merges one owned command handler into each lifecycle event. Existing
// settings and non-owned hook handlers are retained. Repeated installation
// replaces prior owned handlers, including ones pointing at an older binary.
func Install(a Adapter, executable string) (path string, changed bool, err error) {
	path, err = a.SettingsPath()
	if err != nil {
		return "", false, fmt.Errorf("locate %s settings: %w", a.Name(), err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return "", false, fmt.Errorf("resolve executable: %w", err)
	}
	root, old, mode, err := readSettings(path)
	if err != nil {
		return path, false, err
	}
	hooks, err := objectField(root, "hooks", true)
	if err != nil {
		return path, false, fmt.Errorf("%s: %w", path, err)
	}
	for _, event := range installedEvents {
		groups, err := eventGroups(hooks[event], event, true)
		if err != nil {
			return path, false, fmt.Errorf("%s: %w", path, err)
		}
		groups = removeOwned(a, groups)
		handler, err := json.Marshal(a.Handler(executable, event))
		if err != nil {
			return path, false, err
		}
		group, err := json.Marshal(map[string]any{"hooks": []json.RawMessage{handler}})
		if err != nil {
			return path, false, err
		}
		groups = append(groups, group)
		hooks[event], _ = json.Marshal(groups)
	}
	root["hooks"], _ = json.Marshal(hooks)
	next, err := marshalSettings(root)
	if err != nil {
		return path, false, err
	}
	if bytes.Equal(old, next) {
		return path, false, nil
	}
	if err := writeSettings(path, next, mode); err != nil {
		return path, false, err
	}
	return path, true, nil
}

// Uninstall removes only handlers carrying this adapter's ownership marker.
// A missing settings file is already uninstalled and remains untouched.
func Uninstall(a Adapter) (path string, changed bool, err error) {
	path, err = a.SettingsPath()
	if err != nil {
		return "", false, fmt.Errorf("locate %s settings: %w", a.Name(), err)
	}
	root, old, mode, err := readSettingsIfExists(path)
	if err != nil || root == nil {
		return path, false, err
	}
	hooks, err := objectField(root, "hooks", false)
	if err != nil || hooks == nil {
		// A non-object hooks value cannot contain one of our valid entries.
		return path, false, nil
	}
	removedAny := false
	for _, event := range installedEvents {
		raw, exists := hooks[event]
		if !exists {
			continue
		}
		groups, err := eventGroups(raw, event, false)
		if err != nil {
			continue
		}
		cleaned := removeOwned(a, groups)
		if rawGroupsEqual(groups, cleaned) {
			continue
		}
		removedAny = true
		if len(cleaned) == 0 {
			// Parent arrays carry no legal ownership field in either harness
			// schema, so retain the empty scaffold rather than risk deleting
			// one the user had before installation.
			hooks[event] = json.RawMessage("[]")
		} else {
			hooks[event], _ = json.Marshal(cleaned)
		}
	}
	if !removedAny {
		return path, false, nil
	}
	root["hooks"], _ = json.Marshal(hooks)
	next, err := marshalSettings(root)
	if err != nil {
		return path, false, err
	}
	if bytes.Equal(old, next) {
		return path, false, nil
	}
	if err := writeSettings(path, next, mode); err != nil {
		return path, false, err
	}
	return path, true, nil
}

type rawObject map[string]json.RawMessage

func readSettings(path string) (rawObject, []byte, os.FileMode, error) {
	root, old, mode, err := readSettingsIfExists(path)
	if err != nil {
		return nil, nil, 0, err
	}
	if root == nil {
		root = rawObject{}
		old = nil
		mode = 0o600
	}
	return root, old, mode, nil
}

func readSettingsIfExists(path string) (rawObject, []byte, os.FileMode, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, 0o600, nil
		}
		return nil, nil, 0, fmt.Errorf("inspect %s settings: %w", filepath.Base(filepath.Dir(path)), err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, 0, fmt.Errorf("refuse to replace non-regular settings file %s", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("read settings %s: %w", path, err)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return rawObject{}, b, info.Mode().Perm(), nil
	}
	var root rawObject
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, nil, 0, fmt.Errorf("parse settings %s: %w", path, err)
	}
	if root == nil {
		return nil, nil, 0, fmt.Errorf("parse settings %s: top level must be a JSON object", path)
	}
	return root, b, info.Mode().Perm(), nil
}

func objectField(root rawObject, field string, create bool) (rawObject, error) {
	raw, ok := root[field]
	if !ok {
		if create {
			return rawObject{}, nil
		}
		return nil, nil
	}
	var obj rawObject
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("%s must be a JSON object", field)
	}
	return obj, nil
}

func eventGroups(raw json.RawMessage, event string, create bool) ([]json.RawMessage, error) {
	if len(raw) == 0 {
		if create {
			return []json.RawMessage{}, nil
		}
		return nil, nil
	}
	var groups []json.RawMessage
	if err := json.Unmarshal(raw, &groups); err != nil || groups == nil {
		return nil, fmt.Errorf("hooks.%s must be a JSON array", event)
	}
	return groups, nil
}

func removeOwned(a Adapter, groups []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(groups))
	for _, groupRaw := range groups {
		var group rawObject
		if json.Unmarshal(groupRaw, &group) != nil || group == nil {
			out = append(out, groupRaw)
			continue
		}
		hooksRaw, ok := group["hooks"]
		if !ok {
			out = append(out, groupRaw)
			continue
		}
		var handlers []json.RawMessage
		if json.Unmarshal(hooksRaw, &handlers) != nil || handlers == nil {
			out = append(out, groupRaw)
			continue
		}
		kept := handlers[:0]
		removed := false
		for _, handler := range handlers {
			if a.OwnsHandler(handler) {
				removed = true
				continue
			}
			kept = append(kept, handler)
		}
		if !removed {
			out = append(out, groupRaw)
			continue
		}
		if len(kept) == 0 && len(group) == 1 {
			continue
		}
		group["hooks"], _ = json.Marshal(kept)
		updated, _ := json.Marshal(group)
		out = append(out, updated)
	}
	return out
}

func hasCanonicalGroup(groups []json.RawMessage, want json.RawMessage) bool {
	var canonical any
	if json.Unmarshal(want, &canonical) != nil {
		return false
	}
	for _, groupRaw := range groups {
		var candidate any
		if json.Unmarshal(groupRaw, &candidate) == nil && reflect.DeepEqual(candidate, canonical) {
			return true
		}
	}
	return false
}

func rawGroupsEqual(a, b []json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func marshalSettings(root rawObject) ([]byte, error) {
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode settings: %w", err)
	}
	return append(b, '\n'), nil
}

func writeSettings(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create settings directory %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".nine-tails-settings-*")
	if err != nil {
		return fmt.Errorf("create temporary settings file: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if mode == 0 {
		mode = 0o600
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return fmt.Errorf("set temporary settings permissions: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write temporary settings: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync temporary settings: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temporary settings: %w", err)
	}
	if err := replaceFile(tmp, path); err != nil {
		return fmt.Errorf("replace settings %s: %w", path, err)
	}
	return nil
}
