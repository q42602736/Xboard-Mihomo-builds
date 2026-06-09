package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type GitHubClient struct {
	Token  string
	Owner  string
	Repo   string
	Branch string
}

const (
	githubBranchesCacheTTL           = 10 * time.Minute
	githubActiveWorkflowRunsCacheTTL = 12 * time.Second
	githubRecentWorkflowRunsCacheTTL = 20 * time.Second
	githubWorkflowRunCacheTTL        = 8 * time.Second
	githubWorkflowJobsCacheTTL       = 12 * time.Second
	githubReleaseCacheTTL            = 30 * time.Second
	githubReleaseMissCacheTTL        = 8 * time.Second
	githubCommitsCacheTTL            = 2 * time.Minute
)

var (
	githubBranchesCache           keyedTTLCache[[]Branch]
	githubActiveWorkflowRunsCache keyedTTLCache[[]WorkflowRun]
	githubRecentWorkflowRunsCache keyedTTLCache[[]WorkflowRun]
	githubWorkflowRunCache        keyedTTLCache[workflowRunCacheValue]
	githubWorkflowJobsCache       keyedTTLCache[[]WorkflowJob]
	githubReleaseCache            keyedTTLCache[releaseCacheValue]
	githubCommitsCache            keyedTTLCache[[]CommitInfo]
)

type workflowRunCacheValue struct {
	Run    *WorkflowRun
	Inputs map[string]string
}

type releaseCacheValue struct {
	Release *Release
}

func githubReleaseCacheKey(buildOwner, buildRepo, tag string) string {
	return fmt.Sprintf("%s/%s|%s", buildOwner, buildRepo, tag)
}

func NewGitHubClient(token, owner, repo, branch string) *GitHubClient {
	return &GitHubClient{Token: token, Owner: owner, Repo: repo, Branch: branch}
}

func (g *GitHubClient) apiURL(path string) string {
	return fmt.Sprintf("https://api.github.com/repos/%s/%s/%s", g.Owner, g.Repo, path)
}

func escapeGitHubContentPath(filePath string) string {
	parts := strings.Split(filePath, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func escapeGitHubRefName(refName string) string {
	return escapeGitHubContentPath(strings.TrimSpace(refName))
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
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return http.DefaultClient.Do(req)
}

func githubAPIError(resp *http.Response, respBody []byte, action string) error {
	action = strings.TrimSpace(action)
	if action == "" {
		action = "GitHub API 错误"
	}

	bodyText := strings.TrimSpace(string(respBody))
	if message := githubRateLimitMessage(resp, bodyText); message != "" {
		return fmt.Errorf("%s: %s", action, message)
	}

	if resp == nil {
		if bodyText == "" {
			bodyText = "请求失败"
		}
		return fmt.Errorf("%s: %s", action, bodyText)
	}
	if bodyText == "" {
		bodyText = http.StatusText(resp.StatusCode)
	}
	return fmt.Errorf("%s (%d): %s", action, resp.StatusCode, bodyText)
}

func githubRateLimitMessage(resp *http.Response, bodyText string) string {
	if resp == nil {
		return ""
	}
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return ""
	}

	lowerBody := strings.ToLower(bodyText)
	remaining := strings.TrimSpace(resp.Header.Get("X-RateLimit-Remaining"))
	if remaining != "0" && !strings.Contains(lowerBody, "rate limit") {
		return ""
	}

	limit := strings.TrimSpace(resp.Header.Get("X-RateLimit-Limit"))
	resource := strings.TrimSpace(resp.Header.Get("X-RateLimit-Resource"))
	resetRaw := strings.TrimSpace(resp.Header.Get("X-RateLimit-Reset"))

	message := "GitHub API 额度已用完"
	if strings.Contains(lowerBody, "secondary rate limit") {
		message = "GitHub API 触发二级限流"
	}

	details := []string{}
	if resource != "" {
		details = append(details, "资源类型 "+resource)
	}
	if limit != "" {
		details = append(details, "每小时额度 "+limit+" 次")
	}
	if remaining != "" {
		details = append(details, "剩余额度 "+remaining+" 次")
	}
	if len(details) > 0 {
		message += "（" + strings.Join(details, "，") + "）"
	}

	if resetUnix, err := strconv.ParseInt(resetRaw, 10, 64); err == nil && resetUnix > 0 {
		resetAt := time.Unix(resetUnix, 0)
		localText := resetAt.Local().Format("2006-01-02 15:04:05 MST")
		utcText := resetAt.UTC().Format("2006-01-02 15:04:05 UTC")
		wait := time.Until(resetAt)
		if wait > 0 {
			waitMinutes := int64((wait + time.Minute - time.Nanosecond) / time.Minute)
			message += fmt.Sprintf("，预计 %s 恢复（约 %d 分钟后，UTC %s）", localText, waitMinutes, utcText)
		} else {
			message += fmt.Sprintf("，预计 %s 恢复（UTC %s，可现在重试）", localText, utcText)
		}
	} else {
		message += "，请稍后再试"
	}

	return message + "。请先暂停刷新或重复操作，等额度恢复后再加载。"
}

type ghFileResponse struct {
	Content     string `json:"content"`
	SHA         string `json:"sha"`
	Encoding    string `json:"encoding"`
	DownloadURL string `json:"download_url"`
}

type ghRefResponse struct {
	Object struct {
		SHA string `json:"sha"`
	} `json:"object"`
}

func (g *GitHubClient) doRawGet(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "token "+g.Token)
	req.Header.Set("Accept", "application/vnd.github.raw")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, githubAPIError(resp, respBody, "读取 GitHub 文件失败")
	}

	return io.ReadAll(resp.Body)
}

func (g *GitHubClient) GetFileBytes(path string) (content []byte, sha string, err error) {
	url := g.apiURL("contents/" + escapeGitHubContentPath(path) + "?ref=" + url.QueryEscape(g.Branch))
	resp, err := g.doRequest("GET", url, nil)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, "", githubAPIError(resp, respBody, "读取 GitHub 文件失败")
	}

	var fc ghFileResponse
	if err := json.NewDecoder(resp.Body).Decode(&fc); err != nil {
		return nil, "", err
	}

	// GitHub Contents API 对 1-100MB 文件在默认 JSON 响应里会返回空 content，
	// 需要改用 raw 媒体类型重新获取真实字节内容。
	if strings.TrimSpace(fc.Content) == "" || strings.EqualFold(strings.TrimSpace(fc.Encoding), "none") {
		rawBytes, err := g.doRawGet(url)
		if err != nil {
			return nil, "", err
		}
		return rawBytes, fc.SHA, nil
	}

	cleaned := strings.ReplaceAll(fc.Content, "\n", "")
	decoded, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return nil, "", err
	}

	return decoded, fc.SHA, nil
}

// GetFile 从 GitHub 仓库读取文件，返回解码后的内容和 SHA
func (g *GitHubClient) GetFile(path string) (content string, sha string, err error) {
	data, sha, err := g.GetFileBytes(path)
	if err != nil {
		return "", "", err
	}
	return string(data), sha, nil
}

func (g *GitHubClient) EnsureBranchFromDefault() error {
	targetBranch := strings.TrimSpace(g.Branch)
	if targetBranch == "" {
		return fmt.Errorf("目标分支不能为空")
	}

	refURL := g.apiURL("git/ref/heads/" + escapeGitHubRefName(targetBranch))
	resp, err := g.doRequest("GET", refURL, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusOK {
		resp.Body.Close()
		return nil
	}
	if resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return githubAPIError(resp, respBody, "检查分支失败")
	}
	resp.Body.Close()

	branches, err := g.ListBranchesForRepo(g.Owner, g.Repo)
	if err != nil {
		return err
	}
	sourceBranch := ""
	if len(branches) > 0 {
		sourceBranch = branches[0].Name
	}
	for _, branch := range branches {
		if branch.Name == "main" || branch.Name == "master" {
			sourceBranch = branch.Name
			break
		}
	}
	if sourceBranch == "" {
		return fmt.Errorf("无法找到可用于创建分支的默认分支")
	}

	sourceURL := g.apiURL("git/ref/heads/" + escapeGitHubRefName(sourceBranch))
	sourceResp, err := g.doRequest("GET", sourceURL, nil)
	if err != nil {
		return err
	}
	defer sourceResp.Body.Close()
	if sourceResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(sourceResp.Body)
		return githubAPIError(sourceResp, respBody, "读取默认分支失败")
	}
	var sourceRef ghRefResponse
	if err := json.NewDecoder(sourceResp.Body).Decode(&sourceRef); err != nil {
		return err
	}
	if strings.TrimSpace(sourceRef.Object.SHA) == "" {
		return fmt.Errorf("默认分支 SHA 为空")
	}

	createBody := map[string]string{
		"ref": "refs/heads/" + targetBranch,
		"sha": sourceRef.Object.SHA,
	}
	createResp, err := g.doRequest("POST", g.apiURL("git/refs"), createBody)
	if err != nil {
		return err
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated && createResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(createResp.Body)
		if createResp.StatusCode == http.StatusUnprocessableEntity && strings.Contains(string(respBody), "Reference already exists") {
			return nil
		}
		return githubAPIError(createResp, respBody, "创建分支失败")
	}
	githubBranchesCache.delete(fmt.Sprintf("%s/%s", g.Owner, g.Repo))
	return nil
}

// SaveFile 创建或更新 GitHub 仓库中的文件。sha 为空时创建新文件，非空时更新。
func (g *GitHubClient) SaveFile(path, content, sha, message string) (string, error) {
	url := g.apiURL("contents/" + escapeGitHubContentPath(path))
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
		return "", githubAPIError(resp, respBody, "保存 GitHub 文件失败")
	}

	var result struct {
		Content struct {
			SHA string `json:"sha"`
		} `json:"content"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	return result.Content.SHA, nil
}

func (g *GitHubClient) DeleteFile(path, sha, message string) error {
	url := g.apiURL("contents/" + escapeGitHubContentPath(path))
	body := map[string]string{
		"message": message,
		"sha":     sha,
		"branch":  g.Branch,
	}

	resp, err := g.doRequest("DELETE", url, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return githubAPIError(resp, respBody, "删除 GitHub 文件失败")
	}

	return nil
}

type GitHubContentItem struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

func (g *GitHubClient) ListDirectory(dirPath string) ([]GitHubContentItem, error) {
	apiURL := g.apiURL("contents/" + escapeGitHubContentPath(dirPath) + "?ref=" + url.QueryEscape(g.Branch))
	resp, err := g.doRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, githubAPIError(resp, respBody, "读取 GitHub 目录失败")
	}

	var items []GitHubContentItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, err
	}
	return items, nil
}

func (g *GitHubClient) GetLatestCommitTime(filePath string) (string, error) {
	apiURL := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/commits?sha=%s&path=%s&per_page=1",
		g.Owner,
		g.Repo,
		url.QueryEscape(g.Branch),
		url.QueryEscape(filePath),
	)
	resp, err := g.doRequest("GET", apiURL, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", githubAPIError(resp, respBody, "读取 GitHub 提交时间失败")
	}

	var commits []struct {
		Commit struct {
			Committer struct {
				Date string `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&commits); err != nil {
		return "", err
	}
	if len(commits) == 0 || commits[0].Commit.Committer.Date == "" {
		return "", nil
	}

	t, err := time.Parse(time.RFC3339, commits[0].Commit.Committer.Date)
	if err != nil {
		return commits[0].Commit.Committer.Date, nil
	}
	return t.In(time.FixedZone("CST", 8*3600)).Format("2006/1/2 15:04:05"), nil
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
		return githubAPIError(resp, respBody, "触发构建失败")
	}

	return nil
}

type Branch struct {
	Name string `json:"name"`
}

type CommitInfo struct {
	SHA         string `json:"sha"`
	ShortSHA    string `json:"short_sha"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Message     string `json:"message"`
	AuthorName  string `json:"author_name"`
	CommittedAt string `json:"committed_at"`
	HTMLURL     string `json:"html_url"`
}

// ListBranches 获取仓库的分支列表
func (g *GitHubClient) ListBranches() ([]Branch, error) {
	return g.ListBranchesForRepo(g.Owner, g.Repo)
}

// ListBranchesForRepo 获取指定仓库的分支列表
func (g *GitHubClient) ListBranchesForRepo(owner, repo string) ([]Branch, error) {
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("仓库信息不能为空")
	}

	cacheKey := fmt.Sprintf("%s/%s", owner, repo)
	if branches, ok := githubBranchesCache.get(cacheKey); ok {
		return branches, nil
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/branches", owner, repo)
	resp, err := g.doRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, githubAPIError(resp, respBody, "获取分支列表失败")
	}

	var branches []Branch
	json.NewDecoder(resp.Body).Decode(&branches)
	githubBranchesCache.set(cacheKey, branches, githubBranchesCacheTTL)
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

// GetActiveWorkflowRuns 获取指定 workflow 当前仍处于活动状态的运行实例
func (g *GitHubClient) GetActiveWorkflowRuns(buildOwner, buildRepo, workflow string) ([]WorkflowRun, error) {
	cacheKey := fmt.Sprintf("%s/%s|%s", buildOwner, buildRepo, workflow)
	if runs, ok := githubActiveWorkflowRunsCache.get(cacheKey); ok {
		return runs, nil
	}

	activeStatuses := []string{"in_progress", "queued", "waiting", "pending", "requested"}
	runMap := make(map[int64]WorkflowRun)

	for _, status := range activeStatuses {
		url := fmt.Sprintf(
			"https://api.github.com/repos/%s/%s/actions/workflows/%s/runs?status=%s&per_page=100&event=workflow_dispatch",
			buildOwner, buildRepo, workflow, status,
		)
		resp, err := g.doRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}

		var result struct {
			WorkflowRuns []WorkflowRun `json:"workflow_runs"`
		}
		err = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}

		for _, run := range result.WorkflowRuns {
			runMap[run.ID] = run
		}
	}

	runs := make([]WorkflowRun, 0, len(runMap))
	for _, run := range runMap {
		runs = append(runs, run)
	}
	githubActiveWorkflowRunsCache.set(cacheKey, runs, githubActiveWorkflowRunsCacheTTL)
	return runs, nil
}

// GetRecentWorkflowRuns 获取最近的工作流运行列表
func (g *GitHubClient) GetRecentWorkflowRuns(buildOwner, buildRepo, workflow string, count int) ([]WorkflowRun, error) {
	cacheKey := fmt.Sprintf("%s/%s|%s|%d", buildOwner, buildRepo, workflow, count)
	if runs, ok := githubRecentWorkflowRunsCache.get(cacheKey); ok {
		return runs, nil
	}

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
		respBody, _ := io.ReadAll(resp.Body)
		return nil, githubAPIError(resp, respBody, "获取工作流运行列表失败")
	}

	var result struct {
		WorkflowRuns []WorkflowRun `json:"workflow_runs"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	githubRecentWorkflowRunsCache.set(cacheKey, result.WorkflowRuns, githubRecentWorkflowRunsCacheTTL)
	return result.WorkflowRuns, nil
}

// GetWorkflowRun 通过 run_id 直接获取单个 run 的状态
func (g *GitHubClient) GetWorkflowRun(buildOwner, buildRepo string, runID int64) (*WorkflowRun, map[string]string, error) {
	cacheKey := fmt.Sprintf("%s/%s|%d", buildOwner, buildRepo, runID)
	if cached, ok := githubWorkflowRunCache.get(cacheKey); ok {
		if cached.Run != nil {
			return cached.Run, cached.Inputs, nil
		}
	}

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
		respBody, _ := io.ReadAll(resp.Body)
		return nil, nil, githubAPIError(resp, respBody, "获取运行详情失败")
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

	githubWorkflowRunCache.set(cacheKey, workflowRunCacheValue{
		Run:    &run,
		Inputs: inputs,
	}, githubWorkflowRunCacheTTL)

	return &run, inputs, nil
}

// GetWorkflowRunJobs 获取指定运行的 job 列表
func (g *GitHubClient) GetWorkflowRunJobs(buildOwner, buildRepo string, runID int64) ([]WorkflowJob, error) {
	cacheKey := fmt.Sprintf("%s/%s|%d", buildOwner, buildRepo, runID)
	if jobs, ok := githubWorkflowJobsCache.get(cacheKey); ok {
		return jobs, nil
	}

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
		respBody, _ := io.ReadAll(resp.Body)
		return nil, githubAPIError(resp, respBody, "获取 job 列表失败")
	}

	var result struct {
		Jobs []WorkflowJob `json:"jobs"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	githubWorkflowJobsCache.set(cacheKey, result.Jobs, githubWorkflowJobsCacheTTL)
	return result.Jobs, nil
}

func (g *GitHubClient) DeleteWorkflowRun(buildOwner, buildRepo string, runID int64) error {
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/actions/runs/%d",
		buildOwner, buildRepo, runID,
	)
	resp, err := g.doRequest("DELETE", url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		return nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	return githubAPIError(resp, respBody, "删除 workflow 运行失败")
}

func (g *GitHubClient) forceCancelWorkflowRun(buildOwner, buildRepo string, runID int64) error {
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/actions/runs/%d/force-cancel",
		buildOwner, buildRepo, runID,
	)
	resp, err := g.doRequest("POST", url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		return nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	return githubAPIError(resp, respBody, "强制停止 workflow 运行失败")
}

func (g *GitHubClient) CancelWorkflowRun(buildOwner, buildRepo string, runID int64) error {
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/actions/runs/%d/cancel",
		buildOwner, buildRepo, runID,
	)
	resp, err := g.doRequest("POST", url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode == http.StatusConflict {
		return g.forceCancelWorkflowRun(buildOwner, buildRepo, runID)
	}

	respBody, _ := io.ReadAll(resp.Body)
	return githubAPIError(resp, respBody, "停止 workflow 运行失败")
}

// GetWorkflowRunInputs 获取单个 run 的 workflow_dispatch 输入参数
func (g *GitHubClient) GetWorkflowRunInputs(buildOwner, buildRepo string, runID int64) (map[string]string, error) {
	if cached, ok := githubWorkflowRunCache.get(fmt.Sprintf("%s/%s|%d", buildOwner, buildRepo, runID)); ok {
		if cached.Inputs != nil {
			return cached.Inputs, nil
		}
	}

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
		respBody, _ := io.ReadAll(resp.Body)
		return nil, githubAPIError(resp, respBody, "获取运行详情失败")
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	var runDetail map[string]json.RawMessage
	json.Unmarshal(bodyBytes, &runDetail)

	inputs := make(map[string]string)
	if rawInputs, ok := runDetail["inputs"]; ok {
		json.Unmarshal(rawInputs, &inputs)
	}
	githubWorkflowRunCache.set(fmt.Sprintf("%s/%s|%d", buildOwner, buildRepo, runID), workflowRunCacheValue{
		Run:    nil,
		Inputs: inputs,
	}, githubWorkflowRunCacheTTL)
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
	cacheKey := githubReleaseCacheKey(buildOwner, buildRepo, tag)
	if cached, ok := githubReleaseCache.get(cacheKey); ok {
		return cached.Release, nil
	}

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
		githubReleaseCache.set(cacheKey, releaseCacheValue{Release: &release}, githubReleaseCacheTTL)
		return &release, nil
	}

	if resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, githubAPIError(resp, respBody, "获取 Release 失败")
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
		return nil, githubAPIError(listResp, respBody, "获取 Release 列表失败")
	}

	var releases []Release
	if err := json.NewDecoder(listResp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	for i := range releases {
		if releases[i].TagName == tag {
			githubReleaseCache.set(cacheKey, releaseCacheValue{Release: &releases[i]}, githubReleaseCacheTTL)
			return &releases[i], nil
		}
	}
	githubReleaseCache.set(cacheKey, releaseCacheValue{Release: nil}, githubReleaseMissCacheTTL)
	return nil, nil
}

func (g *GitHubClient) DeleteRelease(buildOwner, buildRepo string, releaseID int64) error {
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/releases/%d",
		buildOwner, buildRepo, releaseID,
	)
	resp, err := g.doRequest("DELETE", url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		return nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	return githubAPIError(resp, respBody, "删除 Release 失败")
}

func (g *GitHubClient) DeleteReleaseByTag(buildOwner, buildRepo, tag string) error {
	release, err := g.GetReleaseByTag(buildOwner, buildRepo, tag)
	if err != nil {
		return err
	}
	if release == nil {
		githubReleaseCache.delete(githubReleaseCacheKey(buildOwner, buildRepo, tag))
		return nil
	}
	if err := g.DeleteRelease(buildOwner, buildRepo, release.ID); err != nil {
		return err
	}
	githubReleaseCache.delete(githubReleaseCacheKey(buildOwner, buildRepo, tag))
	return nil
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
		return nil, githubAPIError(resp, respBody, "下载 Release 资源失败")
	}
	return resp, nil
}

func (g *GitHubClient) ListRecentCommits(branch string, count int) ([]CommitInfo, error) {
	targetBranch := strings.TrimSpace(branch)
	if targetBranch == "" {
		targetBranch = g.Branch
	}
	return g.ListRecentCommitsForRepo(g.Owner, g.Repo, targetBranch, count)
}

func (g *GitHubClient) ListRecentCommitsForRepo(owner, repo, branch string, count int) ([]CommitInfo, error) {
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("仓库信息不能为空")
	}

	if count <= 0 {
		count = defaultClientUpdatesLimit
	}
	if count > 100 {
		count = 100
	}

	targetBranch := strings.TrimSpace(branch)
	cacheBranch := targetBranch
	if cacheBranch == "" {
		cacheBranch = "<default>"
	}
	cacheKey := fmt.Sprintf("%s/%s|%s|%d", owner, repo, cacheBranch, count)
	if commits, ok := githubCommitsCache.get(cacheKey); ok {
		return commits, nil
	}

	perPage := count
	if perPage < 30 {
		perPage = 30
	}
	if perPage > 100 {
		perPage = 100
	}

	shouldHideCommitTitle := func(title string) bool {
		return strings.HasPrefix(title, "保存配置档案:") ||
			strings.HasPrefix(title, "保存配置档案：") ||
			strings.HasPrefix(title, "更新打包版本记录:") ||
			strings.HasPrefix(title, "更新打包版本记录：")
	}

	commits := make([]CommitInfo, 0, count)
	for page := 1; len(commits) < count; page++ {
		apiURL := fmt.Sprintf(
			"https://api.github.com/repos/%s/%s/commits?per_page=%d&page=%d",
			owner,
			repo,
			perPage,
			page,
		)
		if targetBranch != "" {
			apiURL += "&sha=" + url.QueryEscape(targetBranch)
		}
		resp, err := g.doRequest("GET", apiURL, nil)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, githubAPIError(resp, respBody, "获取提交记录失败")
		}

		var rawCommits []struct {
			SHA     string `json:"sha"`
			HTMLURL string `json:"html_url"`
			Commit  struct {
				Message string `json:"message"`
				Author  struct {
					Name string `json:"name"`
					Date string `json:"date"`
				} `json:"author"`
			} `json:"commit"`
			Author *struct {
				Login string `json:"login"`
			} `json:"author"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&rawCommits); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		if len(rawCommits) == 0 {
			break
		}

		for _, item := range rawCommits {
			message := strings.ReplaceAll(item.Commit.Message, "\r\n", "\n")
			parts := strings.SplitN(message, "\n", 2)
			title := strings.TrimSpace(parts[0])
			if shouldHideCommitTitle(title) {
				continue
			}

			description := ""
			if len(parts) > 1 {
				description = strings.TrimSpace(parts[1])
			}
			authorName := strings.TrimSpace(item.Commit.Author.Name)
			if authorName == "" && item.Author != nil {
				authorName = item.Author.Login
			}
			shortSHA := item.SHA
			if len(shortSHA) > 7 {
				shortSHA = shortSHA[:7]
			}

			commits = append(commits, CommitInfo{
				SHA:         item.SHA,
				ShortSHA:    shortSHA,
				Title:       title,
				Description: description,
				Message:     message,
				AuthorName:  authorName,
				CommittedAt: item.Commit.Author.Date,
				HTMLURL:     item.HTMLURL,
			})
			if len(commits) >= count {
				break
			}
		}

		if len(rawCommits) < perPage || page >= 20 {
			break
		}
	}

	githubCommitsCache.set(cacheKey, commits, githubCommitsCacheTTL)
	return commits, nil
}
