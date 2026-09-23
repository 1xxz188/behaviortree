// bttool 提供独立本地 Web 编辑器及可用于脚本的元数据生成命令。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/1xxz188/behaviortree/codegen"
	"github.com/1xxz188/behaviortree/editor"
	"github.com/1xxz188/behaviortree/model"
)

// main 保持错误退出码可供脚本判断。
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run 将所有命令绑定到同一个验证器和生成器。
func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: bttool <serve|init|validate|generate|catalog> [选项]")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	file := flags.String("file", "project.json", "工程 JSON 文件")
	out := flags.String("out", "", "相对于当前工作目录的生成包路径，默认使用工程 packagePath")
	workspace := flags.String("workspace", ".", "Web 编辑器工作目录")
	addr := flags.String("addr", "127.0.0.1:8791", "本地监听地址")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("不支持的位置参数: %v", flags.Args())
	}
	switch args[0] {
	case "serve":
		if !editor.ValidListenAddr(*addr) {
			return fmt.Errorf("编辑器只允许回环监听地址，例如 127.0.0.1:8791")
		}
		s, err := editor.New(*workspace)
		if err != nil {
			return err
		}
		defer s.Close()
		server := &http.Server{Addr: *addr, Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute}
		fmt.Printf("行为树编辑器: http://%s\n工程目录: %s\n", *addr, *workspace)
		return server.ListenAndServe()
	case "init":
		data, err := model.Encode(model.Example())
		if err != nil {
			return err
		}
		f, err := os.OpenFile(*file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		return err
	case "validate", "generate", "catalog":
		data, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		if len(data) > editor.MaxProjectBytes {
			return fmt.Errorf("工程超过 %d 字节", editor.MaxProjectBytes)
		}
		p, err := model.Decode(data)
		if err != nil {
			return err
		}
		if args[0] == "catalog" {
			data, err = model.ExportCatalog(p)
			if err == nil {
				_, err = os.Stdout.Write(append(data, '\n'))
			}
			return err
		}
		var target model.ResolvedGoPackage
		if args[0] == "generate" {
			// 显式空路径也交给统一解析器拒绝；未传 --out 才沿用工程配置。
			packagePath := p.Generation.PackagePath
			flags.Visit(func(f *flag.Flag) {
				if f.Name == "out" {
					packagePath = *out
				}
			})
			target, err = model.ResolveGoPackage(".", packagePath)
			if err != nil {
				return err
			}
			p.Generation.PackagePath = target.PackagePath
		}
		if diagnostics := model.Validate(p); len(diagnostics) != 0 {
			_ = json.NewEncoder(os.Stdout).Encode(diagnostics)
			return fmt.Errorf("校验失败，共 %d 个问题", len(diagnostics))
		}
		if args[0] == "validate" {
			fmt.Println("校验通过")
			return nil
		}
		root, err := os.OpenRoot(".")
		if err != nil {
			return err
		}
		defer root.Close()
		p, context, err := editor.PrepareProjectContext(root, p)
		if err != nil {
			return err
		}
		result, err := codegen.Generate(p)
		if err != nil {
			return err
		}
		if err = context.Ensure(root); err != nil {
			return err
		}
		if err = editor.WriteProjectGenerated(".", target.PackagePath, result); err != nil {
			return err
		}
		fmt.Printf("已生成到 %s，共 %d 个 Go 文件，版本 %s\n", target.OutputDir, len(result.Files), result.Version)
		return nil
	default:
		return fmt.Errorf("未知命令 %q", args[0])
	}
}
