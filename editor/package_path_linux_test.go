package editor

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/1xxz188/behaviortree/model"
)

// TestGenerationRejectsNamedPipe 验证 Linux 下业务源码或产物清单为命名管道时立即拒绝，不能阻塞生成。
func TestGenerationRejectsNamedPipe(t *testing.T) {
	for _, name := range []string{"helper.go", "tree_gen.map.json"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			p := model.Example()
			dir := filepath.Join(root, p.Generation.PackagePath)
			if err := os.Mkdir(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(filepath.Join(dir, name), 0600); err != nil {
				t.Fatal(err)
			}
			result := generatedForTest(t, p)
			done := make(chan error, 1)
			go func() { done <- WriteProjectGenerated(root, p.Generation.PackagePath, result) }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("未拒绝命名管道")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("命名管道阻塞了生成")
			}
		})
	}
}
