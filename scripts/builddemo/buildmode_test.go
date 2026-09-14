package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/1xxz188/behaviortree/hotload"
)

// TestBuildModeFlags 验证宿主和插件共用的两种模式具有确定的编译标志。
func TestBuildModeFlags(t *testing.T) {
	for _, mode := range []hotload.BuildMode{hotload.BuildRelease, hotload.BuildDebug} {
		b := builder{mode: mode}
		want := []string{"-mod=readonly", "-trimpath", "-buildvcs=false"}
		if mode == hotload.BuildDebug {
			want = append(want, "-tags=debug", "-gcflags=all=-N -l")
		}
		if got := b.commonFlags(); !reflect.DeepEqual(got, want) {
			t.Fatalf("%v flags: %v", mode, got)
		}
	}
	for _, mode := range []hotload.BuildMode{hotload.InvalidBuildMode, hotload.BuildMode(255)} {
		b := builder{mode: mode}
		if _, err := b.prepare("invalid"); err == nil {
			t.Fatal("invalid mode reached staging")
		}
		if _, err := b.collectShared(nil); err == nil {
			t.Fatal("invalid mode reached shared contract collection")
		}
	}
}

// TestCGOBuildEnvironment 验证布尔值仅在环境边界转为 0/1，并覆盖继承的冲突设置。
func TestCGOBuildEnvironment(t *testing.T) {
	t.Setenv("CGO_ENABLED", "inherited")
	for _, enabled := range []bool{true, false} {
		want := "CGO_ENABLED=0"
		if enabled {
			want = "CGO_ENABLED=1"
		}
		count := 0
		for _, entry := range buildEnvironment("test.work", enabled) {
			if strings.HasPrefix(entry, "CGO_ENABLED=") {
				count++
				if entry != want {
					t.Fatalf("wrong CGO environment: %s", entry)
				}
			}
		}
		if count != 1 {
			t.Fatalf("expected exactly one CGO setting, got %d", count)
		}
	}
}
