// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package export

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// redmineRootInfo は --redmine-root から読み取った情報。
type redmineRootInfo struct {
	Version             string // 例 "7.0.2.stable"
	CipherKey           string // database_cipher_key(マニフェストには書かない)
	AttachmentsPath     string // attachments_storage_path(未設定なら <root>/files)
	CipherKeyConfigured bool
}

var reVersionConst = regexp.MustCompile(`(?m)^\s*(MAJOR|MINOR|TINY)\s*=\s*(\d+)`)
var reBranchConst = regexp.MustCompile(`(?m)^\s*BRANCH\s*=\s*(?:'([^']*)'|"([^"]*)"|nil)`)

// readRedmineRoot は lib/redmine/version.rb と config/configuration.yml を読む。
// configuration.yml は default セクションに env(既定 production)セクションを重ねる(Redmine::Configuration と同じ)。
func readRedmineRoot(root, env string) (*redmineRootInfo, []string, error) {
	var warns []string
	info := &redmineRootInfo{}
	b, err := os.ReadFile(filepath.Join(root, "lib", "redmine", "version.rb"))
	if err != nil {
		return nil, nil, fmt.Errorf("redmine root: %w", err)
	}
	parts := map[string]string{}
	for _, m := range reVersionConst.FindAllStringSubmatch(string(b), -1) {
		if _, dup := parts[m[1]]; !dup {
			parts[m[1]] = m[2]
		}
	}
	if parts["MAJOR"] == "" || parts["MINOR"] == "" || parts["TINY"] == "" {
		return nil, nil, fmt.Errorf("redmine root: cannot parse lib/redmine/version.rb")
	}
	info.Version = parts["MAJOR"] + "." + parts["MINOR"] + "." + parts["TINY"]
	if m := reBranchConst.FindStringSubmatch(string(b)); m != nil {
		if br := m[1] + m[2]; br != "" {
			info.Version += "." + br
		}
	}
	if mm := parts["MAJOR"] + "." + parts["MINOR"]; LookupSchema(mm) == nil {
		warns = append(warns, fmt.Sprintf("redmine root reports version %s (expected %s)", info.Version, supportedReleases()))
	}

	info.AttachmentsPath = filepath.Join(root, "files")
	cb, err := os.ReadFile(filepath.Join(root, "config", "configuration.yml"))
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("redmine root: %w", err)
		}
		return info, warns, nil
	}
	var cfg map[string]map[string]any
	if err := yaml.Unmarshal(cb, &cfg); err != nil {
		warns = append(warns, fmt.Sprintf("config/configuration.yml could not be parsed: %v", err))
		return info, warns, nil
	}
	if env == "" {
		env = "production"
	}
	merged := map[string]any{}
	for k, v := range cfg["default"] {
		merged[k] = v
	}
	for k, v := range cfg[env] {
		merged[k] = v
	}
	if v, ok := merged["database_cipher_key"]; ok && v != nil {
		if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
			info.CipherKey = fmt.Sprint(v)
			info.CipherKeyConfigured = true
		}
	}
	if v, ok := merged["attachments_storage_path"]; ok && v != nil {
		if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
			if !filepath.IsAbs(s) {
				// Redmine はプロセスの cwd(通常 Rails.root)基準で解釈する
				s = filepath.Join(root, s)
			}
			info.AttachmentsPath = s
		}
	}
	return info, warns, nil
}
