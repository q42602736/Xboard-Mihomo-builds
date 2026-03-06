package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	for _, status := range []string{"in_progress", "queued", "waiting", "pending", "requested"} {
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

// WorkflowRun 表示一个 GitHub Actions 工作流运行实例
type WorkflowRun struct {
	ID         int64   `json:"id"`
	Status     string  `json:"status"`
	Conclusion *string `json:"conclusion"`
	HTMLURL    string  `json:"html_url"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
	Name       string  `json:"name"`
	RunNumber  int     `json:"run_number"`
	Event      string  `json:"event"`
}

// WorkflowStep 表示 job 中的一个步骤
type WorkflowStep struct {
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	Conclusion *string `json:"conclusion"`
	Number     int     `json:"number"`
}

// WorkflowJob 表示工作流中的一个 job
type WorkflowJob struct {
	ID         int64          `json:"id"`
	Name       string         `json:"name"`
	Status     string         `json:"status"`
	Conclusion *string        `json:"conclusion"`
	StartedAt  string         `json:"started_at"`
	Steps      []WorkflowStep `json:"steps"`
}

type ReleaseAsset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Label              string `json:"label"`
	State              string `json:"state"`
	ContentType        string `json:"content_type"`
	Size               int64  `json:"size"`
	DownloadCount      int64  `json:"download_count"`
	BrowserDownloadURL string `json:"browser_download_url"`
	UpdatedAt          string `json:"updated_at"`
}

type Release struct {
	ID      int64          `json:"id"`
	TagName string         `json:"tag_name"`
	Name    string         `json:"name"`
	HTMLURL string         `json:"html_url"`
	Draft   bool           `json:"draft"`
	Assets  []ReleaseAsset `json:"assets"`
}

// GetRecentWorkflowRuns 获取最近的工作流运行列表
func (g *GitHubClient) GetRecentWorkflowRuns(buildOwner, buildRepo, workflow string, count int) ([]WorkflowRun, error) {
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/actions/workflows/%s/runs?per_page=%d&event=workflow_dispatch",
		buildOwner, buildRepo, workflow, count,
	)
	resp, err := g.doRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("获取工作流运行列表失败 (%d)", resp.StatusCode)
	}

	var result struct {
		WorkflowRuns []WorkflowRun `json:"workflow_runs"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.WorkflowRuns, nil
}

// GetWorkflowRun 通过 run_id 直接获取单个 run 的状态
func (g *GitHubClient) GetWorkflowRun(buildOwner, buildRepo string, runID int64) (*WorkflowRun, map[string]string, error) {
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/actions/runs/%d",
		buildOwner, buildRepo, runID,
	)
	resp, err := g.doRequest("GET", url, nil)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("获取运行详情失败 (%d)", resp.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)

	var run WorkflowRun
	json.Unmarshal(bodyBytes, &run)

	var rawMap map[string]json.RawMessage
	json.Unmarshal(bodyBytes, &rawMap)
	inputs := make(map[string]string)
	if rawInputs, ok := rawMap["inputs"]; ok {
		json.Unmarshal(rawInputs, &inputs)
	}

	return &run, inputs, nil
}

// GetWorkflowRunJobs 获取指定运行的 job 列表
func (g *GitHubClient) GetWorkflowRunJobs(buildOwner, buildRepo string, runID int64) ([]WorkflowJob, error) {
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/actions/runs/%d/jobs",
		buildOwner, buildRepo, runID,
	)
	resp, err := g.doRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("获取 job 列表失败 (%d)", resp.StatusCode)
	}

	var result struct {
		Jobs []WorkflowJob `json:"jobs"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Jobs, nil
}

// GetWorkflowRunInputs 获取单个 run 的 workflow_dispatch 输入参数
func (g *GitHubClient) GetWorkflowRunInputs(buildOwner, buildRepo string, runID int64) (map[string]string, error) {
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/actions/runs/%d",
		buildOwner, buildRepo, runID,
	)
	resp, err := g.doRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("获取运行详情失败 (%d)", resp.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	var runDetail map[string]json.RawMessage
	json.Unmarshal(bodyBytes, &runDetail)

	inputs := make(map[string]string)
	if rawInputs, ok := runDetail["inputs"]; ok {
		json.Unmarshal(rawInputs, &inputs)
	}
	return inputs, nil
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

func (g *GitHubClient) GetReleaseByTag(buildOwner, buildRepo, tag string) (*Release, error) {
	lookupURL := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/releases/tags/%s",
		buildOwner, buildRepo, url.PathEscape(tag),
	)
	resp, err := g.doRequest("GET", lookupURL, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var release Release
		if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
			return nil, err
		}
		return &release, nil
	}

	if resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("获取 Release 失败 (%d): %s", resp.StatusCode, string(respBody))
	}

	listURL := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/releases?per_page=100",
		buildOwner, buildRepo,
	)
	listResp, err := g.doRequest("GET", listURL, nil)
	if err != nil {
		return nil, err
	}
	defer listResp.Body.Close()

	if listResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(listResp.Body)
		return nil, fmt.Errorf("获取 Release 列表失败 (%d): %s", listResp.StatusCode, string(respBody))
	}

	var releases []Release
	if err := json.NewDecoder(listResp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	for i := range releases {
		if releases[i].TagName == tag {
			return &releases[i], nil
		}
	}
	return nil, nil
}

func (g *GitHubClient) DownloadReleaseAsset(buildOwner, buildRepo string, assetID int64) (*http.Response, error) {
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/releases/assets/%d",
		buildOwner, buildRepo, assetID,
	)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+g.Token)
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusTemporaryRedirect {
			return resp, nil
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("下载 Release 资源失败 (%d): %s", resp.StatusCode, string(respBody))
	}
	return resp, nil
}
