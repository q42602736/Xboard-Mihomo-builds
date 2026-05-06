package main

import (
	"fmt"
	"strings"
)

const profilesDir = "profiles"

func profileFilePath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("档案名称不能为空")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("档案名称非法")
	}
	return profilesDir + "/" + name + ".yaml", nil
}

func (h *Handlers) listStoredProfiles() (map[string]string, error) {
	if cached, ok := storedProfilesCache.get(); ok {
		return cloneStringMap(cached), nil
	}

	items, err := h.profileGH.ListDirectory(profilesDir)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return map[string]string{}, nil
		}
		return nil, err
	}

	profiles := make(map[string]string)
	for _, item := range items {
		if item.Type != "file" || !strings.HasSuffix(item.Name, ".yaml") {
			continue
		}

		name := strings.TrimSuffix(item.Name, ".yaml")
		lastUpdated, err := h.profileGH.GetLatestCommitTime(item.Path)
		if err != nil {
			lastUpdated = ""
		}
		profiles[name] = lastUpdated
	}
	storedProfilesCache.set(cloneStringMap(profiles), profileListCacheTTL)
	return profiles, nil
}

func (h *Handlers) getStoredProfile(name string) (yamlContent string, sha string, lastUpdated string, exists bool, err error) {
	filePath, err := profileFilePath(name)
	if err != nil {
		return "", "", "", false, err
	}

	content, sha, err := h.profileGH.GetFile(filePath)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return "", "", "", false, nil
		}
		return "", "", "", false, err
	}

	lastUpdated, err = h.profileGH.GetLatestCommitTime(filePath)
	if err != nil {
		lastUpdated = ""
	}
	return content, sha, lastUpdated, true, nil
}
