package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/model"
)

// TestDraftNodesCLIWarningDoesNotFail 验证 CLI 校验和生成只报警告、返回成功，并保留原工程草稿。
func TestDraftNodesCLIWarningDoesNotFail(t *testing.T) {
	t.Chdir(t.TempDir())
	project := model.Example()
	project.Trees[0].Nodes = append(project.Trees[0].Nodes, model.Node{ID: "draft", Type: model.NodeAction, CodeName: "ActionDraft"})
	project.Trees[0].Layout["draft"] = model.Position{X: -150, Y: 300}
	data, err := model.Encode(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("project.json", data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"validate", "generate"} {
		t.Run(command, func(t *testing.T) {
			stdout, stderr, err := captureDraftCLIOutput(t, func() error { return run([]string{command, "--file", "project.json"}) })
			if err != nil {
				t.Fatalf("草稿警告使 CLI 失败: %v\n%s\n%s", err, stdout, stderr)
			}
			output := stdout + stderr
			if !strings.Contains(output, "warning") || !strings.Contains(output, "draft") {
				t.Fatalf("CLI 成功但没有展示草稿警告: %s", output)
			}
			if command == "generate" {
				source, err := os.ReadFile(filepath.Join("behavior", "glue.gen.go"))
				if err != nil || strings.Contains(string(source), "ActionDraft") || strings.Contains(string(source), `ID: "draft"`) {
					t.Fatal("CLI 没有正确生成排除草稿的源码", err)
				}
			}
			preserved, err := os.ReadFile("project.json")
			if err != nil || string(preserved) != string(data) {
				t.Fatal("CLI 改写或丢失了工程草稿", err)
			}
		})
	}
}

// TestDraftNodesCLIConnectedErrorsStillFail 验证孤立草稿警告不会掩盖根可达节点的真实错误。
func TestDraftNodesCLIConnectedErrorsStillFail(t *testing.T) {
	t.Chdir(t.TempDir())
	project := model.Example()
	project.Trees[0].Nodes = append(project.Trees[0].Nodes,
		model.Node{ID: "draft", Type: model.NodeAction},
		model.Node{ID: "connected", Type: model.NodeAction},
	)
	project.Trees[0].Nodes[0].Children = append(project.Trees[0].Nodes[0].Children, "connected")
	data, err := model.Encode(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("project.json", data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"validate", "generate"} {
		stdout, stderr, err := captureDraftCLIOutput(t, func() error { return run([]string{command}) })
		if err == nil || !strings.Contains(stdout+stderr, "connected") {
			t.Fatal("CLI 未拒绝或未定位真实错误", command, err, stdout, stderr)
		}
	}
	if _, err := os.Stat(filepath.Join("behavior", "glue.gen.go")); !os.IsNotExist(err) {
		t.Fatal("校验失败仍然发布了源码", err)
	}
}

// captureDraftCLIOutput 捕获命令的两个输出流，使用文件避免管道缓冲阻塞，并在返回前恢复全局句柄。
func captureDraftCLIOutput(t *testing.T, command func() error) (string, string, error) {
	t.Helper()
	dir := t.TempDir()
	stdoutPath, stderrPath := filepath.Join(dir, "stdout"), filepath.Join(dir, "stderr")
	stdout, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	stderr, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	previousOut, previousErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdout, stderr
	defer func() { os.Stdout, os.Stderr = previousOut, previousErr }()
	commandErr := command()
	os.Stdout, os.Stderr = previousOut, previousErr
	output, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	errorOutput, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(output), string(errorOutput), commandErr
}
