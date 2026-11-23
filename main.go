package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"text/template"
	"time"

	"github.com/spf13/pflag"
	"github.com/togettoyou/hub-mirror/pkg"
)

var (
	content    = pflag.StringP("content", "", "", "原始镜像，格式为：{ \"platform\": \"\", \"hub-mirror\": [] }")
	maxContent = pflag.IntP("maxContent", "", 11, "原始镜像个数限制")
	repository = pflag.StringP("repository", "", "", "推送仓库地址，为空默认为 hub.docker.com")
	username   = pflag.StringP("username", "", "", "仓库用户名")
	password   = pflag.StringP("password", "", "", "仓库密码")
	outputPath = pflag.StringP("outputPath", "", "output.md", "结果输出路径")
)

func main() {
	pflag.Parse()

	fmt.Println("验证原始镜像内容")
	var hubMirrors struct {
		Content  []string `json:"hub-mirror"`
		Platform string   `json:"platform"`
	}
	if err := json.Unmarshal([]byte(*content), &hubMirrors); err != nil {
		panic(err)
	}
	if len(hubMirrors.Content) > *maxContent {
		panic("提交的原始镜像个数超出了最大限制")
	}
	fmt.Printf("mirrors: %+v, platform: %+v\n", hubMirrors.Content, hubMirrors.Platform)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()

	fmt.Println("初始化 Docker 客户端")
	cli, err := pkg.NewCli(ctx, *repository, *username, *password, os.Stdout)
	if err != nil {
		panic(err)
	}

	outputs := make([]*pkg.Output, 0)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, source := range hubMirrors.Content {
		source := source
		if source == "" {
			continue
		}
		fmt.Println("开始转换镜像", source)
		wg.Add(1)
		go func() {
			defer wg.Done()
			output, err := cli.PullTagPushImage(ctx, source, hubMirrors.Platform)
			if err != nil {
				fmt.Println(source, "转换异常: ", err)
				return
			}
			mu.Lock()
			outputs = append(outputs, output)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		panic(err)
	}
	if len(outputs) == 0 {
		panic("没有转换成功的镜像")
	}

	tmpl, err := template.ParseFiles("output.tmpl")
	if err != nil {
		panic(err)
	}
	outputFile, err := os.Create(*outputPath)
	if err != nil {
		panic(err)
	}
	defer outputFile.Close()
	if err := tmpl.Execute(outputFile, map[string]interface{}{
		"Outputs": outputs,
		"Repository": *repository,
	}); err != nil {
		panic(err)
	}
}
