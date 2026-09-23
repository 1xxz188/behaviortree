// generate 从 Go 元数据生成示例工程、节点目录、可读 Go 控制流和源码映射。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/1xxz188/behaviortree/codegen"
	"github.com/1xxz188/behaviortree/editor"
	"github.com/1xxz188/behaviortree/examples/definition"
	"github.com/1xxz188/behaviortree/model"
)

// main 只覆盖明确的生成文件，绝不覆盖 actions.go。
func main() {
	root := flag.String("root", ".", "行为树仓库根目录")
	flag.Parse()
	project := definition.Project(0)
	result, err := codegen.Generate(project)
	check(err)
	data, err := model.Encode(project)
	check(err)
	write(filepath.Join(*root, "examples", "project.json"), data)
	catalog, err := model.ExportCatalog(project)
	check(err)
	write(filepath.Join(*root, "examples", "catalog.json"), catalog)
	projectDir := filepath.Join(*root, "examples")
	check(editor.WriteProjectGenerated(projectDir, project.Generation.PackagePath, result))
	fmt.Printf("generated examples/project.json, catalog.json and %d Go files in examples/%s\n", len(result.Files), project.Generation.PackagePath)
}

// write 保存生成输出并保留手写文件。
func write(path string, data []byte) {
	check(os.MkdirAll(filepath.Dir(path), 0755))
	check(os.WriteFile(path, data, 0644))
}

// check 将命令失败作为非零退出码报告给构建脚本。
func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
