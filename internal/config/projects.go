package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Project 已打开的本地工程（会话树一级节点）
type Project struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	OpenedAt int64  `json:"opened_at"`
}

type ProjectStore struct {
	mu       sync.RWMutex
	filePath string
	projects []Project
}

func NewProjectStore() (*ProjectStore, error) {
	dir := UserDataDir()
	_ = os.MkdirAll(dir, 0700)
	s := &ProjectStore{
		filePath: filepath.Join(dir, "projects.json"),
		projects: make([]Project, 0),
	}
	_ = s.load()
	return s, nil
}

func projectName(path string) string {
	clean := filepath.Clean(strings.TrimSpace(path))
	base := filepath.Base(clean)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return clean
	}
	return base
}

func sameProjectPath(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func (s *ProjectStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}
	var list []Project
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	s.projects = list
	return nil
}

func (s *ProjectStore) save() error {
	data, err := json.MarshalIndent(s.projects, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteConfig(s.filePath, data)
}

func (s *ProjectStore) List() []Project {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Project, len(s.projects))
	copy(out, s.projects)
	sort.Slice(out, func(i, j int) bool {
		return out[i].OpenedAt > out[j].OpenedAt
	})
	return out
}

func (s *ProjectStore) Add(path string) error {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	for i, p := range s.projects {
		if sameProjectPath(p.Path, path) {
			s.projects[i].OpenedAt = now
			s.projects[i].Name = projectName(path)
			s.projects[i].Path = path
			return s.save()
		}
	}
	s.projects = append(s.projects, Project{
		Path:     path,
		Name:     projectName(path),
		OpenedAt: now,
	})
	return s.save()
}

func (s *ProjectStore) Remove(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make([]Project, 0, len(s.projects))
	for _, p := range s.projects {
		if !sameProjectPath(p.Path, path) {
			next = append(next, p)
		}
	}
	s.projects = next
	return s.save()
}
