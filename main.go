package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Artifact struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	SizeInBytes        int64  `json:"size_in_bytes"`
	ArchiveDownloadURL string `json:"archive_download_url"`
}

const parts = 8

// 全局状态
var (
	currentCDNURL atomic.Value
	apiURL        string
	apiToken      string
	urlMu         sync.Mutex
	httpClient    = &http.Client{} // 全局 client，由 setupProxy 初始化
)

func main() {
	rawArgs := os.Args[1:]
	setupProxy(rawArgs)

	args := stripGlobalArgs(rawArgs)

	// 如果第一个参数是 URL，走自动解析
	if len(args) >= 1 && (strings.HasPrefix(args[0], "http://") || strings.HasPrefix(args[0], "https://")) {
		token, err := getToken(args)
		if err != nil {
			fmt.Println("获取 token 失败:", err)
			os.Exit(1)
		}
		if err := handleURL(args[0], token); err != nil {
			fmt.Println("失败:", err)
			os.Exit(1)
		}
		return
	}

	if len(args) < 2 {
		fmt.Println("用法:")
		fmt.Println("  ghfast <owner/repo> <artifact_id>")
		fmt.Println("  ghfast run <owner/repo> <run_id>")
		fmt.Println("  ghfast release <owner/repo> <tag> [文件名]")
		fmt.Println("  ghfast <url>")
		os.Exit(1)
	}
	token := getTokenOptional(args)

	// 判断当前命令是否需要 token
	needsToken := true
	if len(args) >= 1 {
		if strings.HasPrefix(args[0], "http://") || strings.HasPrefix(args[0], "https://") {
			if p, err := parseGitHubURL(args[0]); err == nil {
				if p.Kind == "release-asset" || p.Kind == "release-page" {
					needsToken = false
				}
			}
		} else if args[0] == "release" {
			needsToken = false
		}
	}

	if needsToken && token == "" {
		printTokenError()
		os.Exit(1)
	}

	// run 子命令：下载该 run 下所有 artifact
	if args[0] == "run" {
		if len(args) < 3 {
			fmt.Println("用法: ghfast run <owner/repo> <run_id>")
			os.Exit(1)
		}
		repo := args[1]
		runID := args[2]

		arts, err := getRunArtifacts(repo, runID, token)
		if err != nil {
			fmt.Println("获取 run 信息失败:", err)
			os.Exit(1)
		}
		if len(arts) == 0 {
			fmt.Println("该 run 没有 artifact")
			return
		}
		fmt.Printf("找到 %d 个 artifact\n", len(arts))

		for i := range arts {
			art := &arts[i]
			fmt.Printf("\n=== [%d/%d] %s (%.1f MB) ===\n",
				i+1, len(arts), art.Name, float64(art.SizeInBytes)/1024/1024)
			if err := downloadArtifact(art, token); err != nil {
				fmt.Println("下载失败:", err)
			}
		}
		return
	}
	// release 子命令：下载 release 文件
	if args[0] == "release" {
		if len(args) < 3 {
			fmt.Println("用法: ghfast release <owner/repo> <tag> [文件名]")
			os.Exit(1)
		}
		repo := args[1]
		tag := args[2]

		assets, err := getReleaseAssets(repo, tag, token)
		if err != nil {
			fmt.Println("获取 release 信息失败:", err)
			os.Exit(1)
		}
		if len(assets) == 0 {
			fmt.Println("该 release 没有文件")
			return
		}

		// 没指定文件名：列出所有文件
		if len(args) < 4 {
			fmt.Printf("release %s 共有 %d 个文件:\n", tag, len(assets))
			for _, a := range assets {
				fmt.Printf("  %-50s  %.1f MB\n", a.Name, float64(a.Size)/1024/1024)
			}
			fmt.Println("\n用法: ghfast release <owner/repo> <tag> <文件名>")
			return
		}

		// 匹配文件名
		keyword := args[3]
		var matched *ReleaseAsset
		for i := range assets {
			if assets[i].Name == keyword {
				matched = &assets[i]
				break
			}
		}
		if matched == nil {
			for i := range assets {
				if strings.Contains(assets[i].Name, keyword) {
					matched = &assets[i]
					break
				}
			}
		}
		if matched == nil {
			fmt.Printf("没有匹配 %q 的文件，可用文件:\n", keyword)
			for _, a := range assets {
				fmt.Printf("  %s\n", a.Name)
			}
			os.Exit(1)
		}

		fmt.Printf("release 文件: %s  (%.1f MB)\n",
			matched.Name, float64(matched.Size)/1024/1024)
		if err := downloadAsset(repo, matched, token); err != nil {
			fmt.Println("\n下载失败:", err)
			os.Exit(1)
		}
		return
	}
	// 默认：下载单个 artifact
	repo := args[0]
	artifactID := args[1]

	art, err := getArtifact(repo, artifactID, token)
	if err != nil {
		fmt.Println("获取 artifact 信息失败:", err)
		os.Exit(1)
	}
	fmt.Printf("artifact: %s  (%.1f MB)\n", art.Name, float64(art.SizeInBytes)/1024/1024)
	if err := downloadArtifact(art, token); err != nil {
		fmt.Println("\n下载失败:", err)
		os.Exit(1)
	}
}

func getArtifact(repo, id, token string) (*Artifact, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/actions/artifacts/%s", repo, id)
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var art Artifact
	if err := json.NewDecoder(resp.Body).Decode(&art); err != nil {
		return nil, err
	}
	return &art, nil
}

func resolveURL(apiURL, token string) (string, error) {
	var finalURL string
	client := &http.Client{
		Transport: httpClient.Transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			finalURL = req.URL.String()
			return http.ErrUseLastResponse
		},
	}
	req, _ := http.NewRequest("GET", apiURL, nil)
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()

	if finalURL == "" {
		return "", errors.New("API 未返回重定向地址")
	}
	return finalURL, nil
}

// refreshCDN 重新解析 CDN 地址，用于链接过期时恢复
func refreshCDN() error {
	urlMu.Lock()
	defer urlMu.Unlock()

	newURL, err := resolveURL(apiURL, apiToken)
	if err != nil {
		return err
	}
	currentCDNURL.Store(newURL)
	return nil
}

func supportsRange(url string) bool {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Range", "bytes=0-0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == 206
}

func downloadMulti(total int64, outFile string) error {
	chunk := (total + parts - 1) / int64(parts)

	var wg sync.WaitGroup
	errCh := make(chan error, parts)
	done := make(chan struct{})

	go monitor(outFile, total, done)

	for i := 0; i < parts; i++ {
		start := int64(i) * chunk
		if start >= total {
			break
		}
		end := start + chunk - 1
		if end >= total {
			end = total - 1
		}

		wg.Add(1)
		go func(idx int, s, e int64) {
			defer wg.Done()
			partFile := fmt.Sprintf("%s.part%d", outFile, idx)
			if err := downloadPartWithResume(s, e, partFile); err != nil {
				errCh <- fmt.Errorf("分片 %d: %w", idx, err)
			}
		}(i, start, end)
	}

	wg.Wait()
	close(done)

	select {
	case err := <-errCh:
		return err
	default:
	}

	return mergeParts(parts, outFile)
}

// downloadPartWithResume 支持断点续传和链接过期自动恢复
func downloadPartWithResume(start, end int64, partFile string) error {
	expected := end - start + 1

	for attempt := 0; attempt < 3; attempt++ {
		// 检查已下载多少
		var have int64
		if fi, err := os.Stat(partFile); err == nil {
			have = fi.Size()
			if have > expected {
				// 数据异常，删掉重下
				os.Remove(partFile)
				have = 0
			}
			if have == expected {
				return nil // 已完成
			}
		}

		// 本次从 start+have 开始
		reqStart := start + have
		req, _ := http.NewRequest("GET", currentCDNURL.Load().(string), nil)
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", reqStart, end))

		resp, err := httpClient.Do(req)
		if err != nil {
			if attempt < 2 {
				time.Sleep(time.Second)
				continue
			}
			return err
		}

		// 链接过期：刷新 CDN 地址后重试
		if resp.StatusCode == 403 || resp.StatusCode == 401 {
			resp.Body.Close()
			if err := refreshCDN(); err != nil {
				return fmt.Errorf("刷新链接失败: %w", err)
			}
			fmt.Printf("\n[分片 %d] 链接过期，已刷新，继续下载\n", start)
			continue
		}

		if resp.StatusCode != 206 && resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		}

		// 以追加模式打开，从 have 位置继续写
		f, err := os.OpenFile(partFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			resp.Body.Close()
			return err
		}

		_, err = io.Copy(f, resp.Body)
		f.Close()
		resp.Body.Close()

		if err != nil {
			if attempt < 2 {
				time.Sleep(time.Second)
				continue
			}
			return err
		}

		// 检查本分片是否完整
		if fi, err := os.Stat(partFile); err == nil && fi.Size() == expected {
			return nil
		}
	}

	return fmt.Errorf("重试 3 次后仍未完成")
}

func mergeParts(parts int, outFile string) error {
	// 先检查所有分片是否齐全
	for i := 0; i < parts; i++ {
		partFile := fmt.Sprintf("%s.part%d", outFile, i)
		if _, err := os.Stat(partFile); err != nil {
			return fmt.Errorf("缺少分片 %s", partFile)
		}
	}

	f, err := os.Create(outFile)
	if err != nil {
		return err
	}
	defer f.Close()

	for i := 0; i < parts; i++ {
		partFile := fmt.Sprintf("%s.part%d", outFile, i)
		pf, err := os.Open(partFile)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, pf)
		pf.Close()
		if err != nil {
			return err
		}
		os.Remove(partFile)
	}
	return nil
}

func monitor(outFile string, total int64, done <-chan struct{}) {
	start := time.Now()

	// 记录启动时已有的分片大小，续传时扣除
	var initial int64
	for i := 0; i < parts; i++ {
		if fi, err := os.Stat(fmt.Sprintf("%s.part%d", outFile, i)); err == nil {
			initial += fi.Size()
		}
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			var sum int64
			for i := 0; i < parts; i++ {
				if fi, err := os.Stat(fmt.Sprintf("%s.part%d", outFile, i)); err == nil {
					sum += fi.Size()
				}
			}
			pct := float64(sum) / float64(total) * 100

			// 只算本次运行新增的部分
			delta := sum - initial
			if delta < 0 {
				delta = 0
			}
			speed := float64(delta) / time.Since(start).Seconds() / 1024

			fmt.Printf("\r  %.1f%%  %.1f/%.1f MB  %.0f KB/s",
				pct, float64(sum)/1024/1024, float64(total)/1024/1024, speed)
		}
	}
}

func downloadSingle(url string, total int64, outFile string) error {
	req, _ := http.NewRequest("GET", url, nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	f, err := os.Create(outFile)
	if err != nil {
		return err
	}
	defer f.Close()

	var downloaded int64
	start := time.Now()
	lastPrint := time.Now()
	buf := make([]byte, 64*1024)

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			f.Write(buf[:n])
			downloaded += int64(n)
			if time.Since(lastPrint) > 500*time.Millisecond {
				pct := float64(downloaded) / float64(total) * 100
				speed := float64(downloaded) / time.Since(start).Seconds() / 1024
				fmt.Printf("\r  %.1f%%  %.1f/%.1f MB  %.0f KB/s",
					pct, float64(downloaded)/1024/1024, float64(total)/1024/1024, speed)
				lastPrint = time.Now()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// getToken 按优先级获取 token：命令行参数 > 环境变量 > gh 命令
func getToken(args []string) (string, error) {
	// 1. 命令行 --token
	for i, a := range args {
		if a == "--token" && i+1 < len(args) {
			return strings.TrimSpace(args[i+1]), nil
		}
	}

	// 2. 环境变量
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return strings.TrimSpace(t), nil
	}

	// 3. 回退到 gh
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return "", fmt.Errorf("gh 命令不可用，且未提供 --token 或 GITHUB_TOKEN")
	}
	return strings.TrimSpace(string(out)), nil
}

// getTokenOptional 尝试获取 token，拿不到返回空字符串（不报错）
func getTokenOptional(args []string) string {
	t, err := getToken(args)
	if err != nil {
		return ""
	}
	return t
}

func printTokenError() {
	fmt.Println("该操作需要 GitHub token，请任选一种方式：")
	fmt.Println("  1. ghfast ... --token <你的token>")
	fmt.Println("  2. 设置环境变量 GITHUB_TOKEN")
	fmt.Println("  3. 安装 gh 并执行 gh auth login")
}

// setupProxy 根据命令行参数或环境变量配置代理
func setupProxy(args []string) {
	var proxyURL string

	// 1. 命令行 --proxy
	for i, a := range args {
		if a == "--proxy" && i+1 < len(args) {
			proxyURL = args[i+1]
			break
		}
	}

	// 2. 环境变量
	if proxyURL == "" {
		proxyURL = os.Getenv("HTTPS_PROXY")
	}
	if proxyURL == "" {
		proxyURL = os.Getenv("HTTP_PROXY")
	}

	// 3. 直连
	if proxyURL == "" {
		httpClient = &http.Client{}
		return
	}

	u, err := url.Parse(proxyURL)
	if err != nil {
		fmt.Println("代理地址无效:", err)
		os.Exit(1)
	}
	httpClient = &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(u)},
	}
	fmt.Printf("使用代理: %s\n", proxyURL)
}

// downloadArtifact 下载单个 artifact，可被多个命令复用
func downloadArtifact(art *Artifact, token string) error {
	apiURL = art.ArchiveDownloadURL
	apiToken = token

	cdnURL, err := resolveURL(apiURL, token)
	if err != nil {
		return fmt.Errorf("解析下载地址失败: %w", err)
	}
	currentCDNURL.Store(cdnURL)

	outFile := art.Name + ".zip"

	if fi, err := os.Stat(outFile); err == nil && fi.Size() == art.SizeInBytes {
		fmt.Println("文件已存在且完整，跳过下载")
		return nil
	}

	start := time.Now()
	if supportsRange(cdnURL) {
		fmt.Println("支持分段，使用 8 线程下载（可断点续传）")
		if err := downloadMulti(art.SizeInBytes, outFile); err != nil {
			return err
		}
	} else {
		fmt.Println("不支持分段，退回单线程")
		if err := downloadSingle(cdnURL, art.SizeInBytes, outFile); err != nil {
			return err
		}
	}

	fi, _ := os.Stat(outFile)
	if fi.Size() != art.SizeInBytes {
		return fmt.Errorf("大小不符，期望 %d，实际 %d", art.SizeInBytes, fi.Size())
	}
	fmt.Printf("\n完成: %s  (%.1f MB, 耗时 %s)\n",
		outFile, float64(fi.Size())/1024/1024,
		time.Since(start).Round(time.Second))
	return nil
}

// getRunArtifacts 查询某次 run 下的所有 artifact
func getRunArtifacts(repo, runID, token string) ([]Artifact, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/actions/runs/%s/artifacts", repo, runID)
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Artifacts []Artifact `json:"artifacts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Artifacts, nil
}

// ReleaseAsset 对应 GitHub release 里的单个文件
type ReleaseAsset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// getReleaseAssets 查询某个 release 下的所有文件
func getReleaseAssets(repo, tag, token string) ([]ReleaseAsset, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", repo, tag)
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var release struct {
		Assets []ReleaseAsset `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	return release.Assets, nil
}

// downloadAsset 下载单个 release 文件，走 API 拿 CDN 地址
func downloadAsset(repo string, asset *ReleaseAsset, token string) error {
	cdnURL, err := resolveAssetURL(repo, asset.ID, token)
	if err != nil {
		return fmt.Errorf("解析下载地址失败: %w", err)
	}
	currentCDNURL.Store(cdnURL)

	outFile := asset.Name

	if fi, err := os.Stat(outFile); err == nil && fi.Size() == asset.Size {
		fmt.Println("文件已存在且完整，跳过下载")
		return nil
	}

	start := time.Now()
	if supportsRange(cdnURL) {
		fmt.Println("支持分段，使用 8 线程下载（可断点续传）")
		if err := downloadMulti(asset.Size, outFile); err != nil {
			return err
		}
	} else {
		fmt.Println("不支持分段，退回单线程")
		if err := downloadSingle(cdnURL, asset.Size, outFile); err != nil {
			return err
		}
	}

	fi, _ := os.Stat(outFile)
	if fi.Size() != asset.Size {
		return fmt.Errorf("大小不符，期望 %d，实际 %d", asset.Size, fi.Size())
	}
	fmt.Printf("\n完成: %s  (%.1f MB, 耗时 %s)\n",
		outFile, float64(fi.Size())/1024/1024,
		time.Since(start).Round(time.Second))
	return nil
}

// resolveAssetURL 通过 asset ID 调 API 拿 CDN 地址，绕开 github.com 主站
func resolveAssetURL(repo string, assetID int64, token string) (string, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/assets/%d", repo, assetID)

	var finalURL string
	client := &http.Client{
		Transport: httpClient.Transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			finalURL = req.URL.String()
			return http.ErrUseLastResponse
		},
	}
	req, _ := http.NewRequest("GET", apiURL, nil)
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	req.Header.Set("Accept", "application/octet-stream") // 关键：告诉 API 返回文件本体

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()

	if finalURL == "" {
		return "", errors.New("API 未返回重定向地址")
	}
	return finalURL, nil
}

// stripGlobalArgs 把 --proxy / --token 及其值从参数里剔除，剩下的才是位置参数
func stripGlobalArgs(args []string) []string {
	var pos []string
	for i := 0; i < len(args); i++ {
		if (args[i] == "--proxy" || args[i] == "--token") && i+1 < len(args) {
			i++ // 跳过参数值
			continue
		}
		pos = append(pos, args[i])
	}
	return pos
}
