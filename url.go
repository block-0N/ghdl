package main

import (
	"fmt"
	"net/url"
	"strings"
)

type parsedURL struct {
	Kind       string // release-asset / release-page / run / artifact
	Owner      string
	Repo       string
	Tag        string
	Filename   string
	RunID      string
	ArtifactID string
}

func parseGitHubURL(raw string) (*parsedURL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("链接格式无效: %w", err)
	}

	path := strings.TrimPrefix(u.Path, "/")
	parts := strings.Split(path, "/")

	switch u.Host {
	case "api.github.com":
		// /repos/{owner}/{repo}/actions/artifacts/{id}/zip
		if len(parts) >= 6 && parts[0] == "repos" && parts[3] == "actions" && parts[4] == "artifacts" {
			return &parsedURL{
				Kind:       "artifact",
				Owner:      parts[1],
				Repo:       parts[2],
				ArtifactID: parts[5],
			}, nil
		}

	case "github.com", "www.github.com":
		// /{owner}/{repo}/releases/download/{tag}/{filename}
		if len(parts) >= 6 && parts[2] == "releases" && parts[3] == "download" {
			return &parsedURL{
				Kind:     "release-asset",
				Owner:    parts[0],
				Repo:     parts[1],
				Tag:      parts[4],
				Filename: strings.Join(parts[5:], "/"),
			}, nil
		}

		// /{owner}/{repo}/releases/tag/{tag}
		if len(parts) >= 5 && parts[2] == "releases" && parts[3] == "tag" {
			return &parsedURL{
				Kind:  "release-page",
				Owner: parts[0],
				Repo:  parts[1],
				Tag:   parts[4],
			}, nil
		}

		// /{owner}/{repo}/actions/runs/{run_id}
		if len(parts) >= 5 && parts[2] == "actions" && parts[3] == "runs" {
			return &parsedURL{
				Kind:  "run",
				Owner: parts[0],
				Repo:  parts[1],
				RunID: parts[4],
			}, nil
		}
	}

	return nil, fmt.Errorf("不支持的 GitHub 链接格式: %s", raw)
}

func handleURL(rawURL, token string) error {
	p, err := parseGitHubURL(rawURL)
	if err != nil {
		return err
	}
	repo := p.Owner + "/" + p.Repo

	switch p.Kind {
	case "release-asset":
		return downloadReleaseAsset(repo, p.Tag, p.Filename, token)

	case "release-page":
		return handleReleasePage(repo, p.Tag, token)

	case "run":
		return handleRun(repo, p.RunID, token)

	case "artifact":
		art, err := getArtifact(repo, p.ArtifactID, token)
		if err != nil {
			return err
		}
		fmt.Printf("artifact: %s  (%.1f MB)\n", art.Name, float64(art.SizeInBytes)/1024/1024)
		return downloadArtifact(art, token)
	}

	return fmt.Errorf("未处理的链接类型: %s", p.Kind)
}

// downloadReleaseAsset 按文件名在 release 里找到对应文件并下载
func downloadReleaseAsset(repo, tag, filename, token string) error {
	assets, err := getReleaseAssets(repo, tag, token)
	if err != nil {
		return err
	}

	var matched *ReleaseAsset
	for i := range assets {
		if assets[i].Name == filename {
			matched = &assets[i]
			break
		}
	}
	if matched == nil {
		return fmt.Errorf("release %s 中没有文件 %q", tag, filename)
	}

	fmt.Printf("release 文件: %s  (%.1f MB)\n", matched.Name, float64(matched.Size)/1024/1024)
	return downloadAsset(repo, matched, token)
}

// handleReleasePage 处理 release 页面链接：单个文件直接下，多个文件列出
func handleReleasePage(repo, tag, token string) error {
	assets, err := getReleaseAssets(repo, tag, token)
	if err != nil {
		return err
	}
	if len(assets) == 0 {
		fmt.Println("该 release 没有文件")
		return nil
	}

	if len(assets) == 1 {
		a := &assets[0]
		fmt.Printf("release 文件: %s  (%.1f MB)\n", a.Name, float64(a.Size)/1024/1024)
		return downloadAsset(repo, a, token)
	}

	fmt.Printf("release %s 共有 %d 个文件:\n", tag, len(assets))
	for _, a := range assets {
		fmt.Printf("  %-50s  %.1f MB\n", a.Name, float64(a.Size)/1024/1024)
	}
	fmt.Println("\n请指定文件名:")
	fmt.Printf("  ghfast release %s %s <文件名>\n", repo, tag)
	return nil
}

// handleRun 处理 run 页面链接：下载该 run 下所有 artifact
func handleRun(repo, runID, token string) error {
	arts, err := getRunArtifacts(repo, runID, token)
	if err != nil {
		return err
	}
	if len(arts) == 0 {
		fmt.Println("该 run 没有 artifact")
		return nil
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
	return nil
}
