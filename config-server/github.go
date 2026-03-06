package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type GitHubClient struct {
	Token  string
	Owner  string
	Repo   string
	Branch string
}

func NewGitHubClient(token, owner, repo, branch string) *GitHubClient {
	return &GitHubClient{Token: token, Owner: owner, Repo: repo, Branch: branch}
}

func (g *GitHubClient) apiURL(path string) string {
	return fmt.Sprintf("https://api.github.com/repos/%s/%s/%s", g.Owner, g.Repo, path)
}

func (g *GitHubClient) doRequest(method, url string, body interface{}) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "token "+g.Token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return http.DefaultClient.Do(req)
}

type ghFileResponse struct {
	Content string `json:"content"`
	SHA     string `json:"sha"`
}

// GetFile 从 GitHub 仓库读取文件，返回解码后的内容和 SHA
func (g *GitHubClient) GetFile(path string) (content string, sha string, err error) {
	url := g.apiURL("contents/" + path + "?ref=" + g.Branch)
	resp, err := g.doRequest("GET", url, nil)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("GitHub API 错误 (%d): %s", resp.StatusCode, string(respBody))
	}

	var fc ghFileResponse
	if err := json.NewDecoder(resp.Body).Decode(&fc); err != nil {
		return "", "", err
	}

	// GitHub 返回的 base64 可能包含换行符
	cleaned := strings.ReplaceAll(fc.Content, "\n", "")
	decoded, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return "", "", err
	}

	return string(decoded), fc.SHA, nil
}

// SaveFile 创建或更新 GitHub 仓库中的文件。sha 为空时创建新文件，非空时更新。
func (g *GitHubClient) SaveFile(path, content, sha, message string) (string, error) {
	url := g.apiURL("contents/" + path)
	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	body := map[string]string{
		"message": message,
		"content": encoded,
		"branch":  g.Branch,
	}
	if sha != "" {
		body["sha"] = sha
	}

	resp, err := g.doRequest("PUT", url, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GitHub API 错误 (%d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content struct {
			SHA string `json:"sha"`
		} `json:"content"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	return result.Content.SHA, nil
}

// TriggerWorkflow 触发 GitHub Actions 工作流
func (g *GitHubClient) TriggerWorkflow(buildOwner, buildRepo, workflow string, inputs map[string]string) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/workflows/%s/dispatches",
		buildOwner, buildRepo, workflow)

	body := map[string]interface{}{
		"ref":    "main",
		"inputs": inputs,
	}

	resp, err := g.doRequest("POST", url, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 204 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("触发构建失败 (%d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

type Branch struct {
	Name string `json:"name"`
}

// ListBranches 获取仓库的分支列表
func (g *GitHubClient) ListBranches() ([]Branch, error) {
	url := g.apiURL("branches")
	resp, err := g.doRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("获取分支列表失败")
	}

	var branches []Branch
	json.NewDecoder(resp.Body).Decode(&branches)
	return branches, nil
}

// GetRunningWorkflowCount 查询指定仓库中正在运行和排队中的工作流数量
func (g *GitHubClient) GetRunningWorkflowCount(buildOwner, buildRepo, workflow string) (int, error) {
	total := 0
	for _, status := range []string{"in_progress", "queued"} {
		url := fmt.Sprintf(
			"https://api.github.com/repos/%s/%s/actions/workflows/%s/runs?status=%s&per_page=1",
			buildOwner, buildRepo, workflow, status,
		)
		resp, err := g.doRequest("GET", url, nil)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			continue
		}

		var result struct {
			TotalCount int `json:"total_count"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		total += result.TotalCount
	}
	return total, nil
}

// SaveFileWithRetry 带冲突重试的文件保存，解决并发写入 SHA 不匹配的问题
func (g *GitHubClient) SaveFileWithRetry(filePath string, contentFn func(existing string) string, message string, maxRetries int) error {
	for i := 0; i < maxRetries; i++ {
		existing, sha, _ := g.GetFile(filePath)
		newContent := contentFn(existing)
		_, err := g.SaveFile(filePath, newContent, sha, message)
		if err == nil {
			return nil
		}
		if !strings.Contains(err.Error(), "409") && !strings.Contains(err.Error(), "422") {
			return err
		}
		// SHA 冲突，重试
	}
	return fmt.Errorf("保存文件失败: 重试 %d 次后仍有冲突", maxRetries)
}
