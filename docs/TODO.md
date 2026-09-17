请改造当前行为树编辑器 / Go 代码生成逻辑，目标是简化生成目录结构，并保证：

“生成包路径”同时决定：
1. 生成目录
2. 最终 Go package 名
3. 目录最后一级名称必须与 Go package 名一致

请直接修改代码完成，不要只给建议。

====================
一、最终规则
====================

当前类似：

generated/
└─ bt_brawl/
   ├─ glue.gen.go
   ├─ tree_xxx.gen.go
   └─ tree_gen.map.json

这种固定 generated 层级需要取消。

以后用户配置：

生成包路径：
bt_brawl

则生成：

<project>/
└─ bt_brawl/
   ├─ glue.gen.go
   ├─ tree_xxx.gen.go
   └─ tree_gen.map.json

所有生成的 Go 文件：

package bt_brawl


如果配置：

生成包路径：
ai/brawl

则生成：

<project>/
└─ ai/
   └─ brawl/
      ├─ glue.gen.go
      ├─ tree_xxx.gen.go
      └─ tree_gen.map.json

所有生成的 Go 文件：

package brawl


如果配置：

game/ai/npc

则生成：

<project>/
└─ game/
   └─ ai/
      └─ npc/
         ├─ glue.gen.go
         ├─ tree_xxx.gen.go
         └─ tree_gen.map.json

package：

package npc


核心规则：

packagePath = "game/ai/npc"
packageName = path.Base(packagePath)

outputDir =
    filepath.Join(
        projectDir,
        filepath.FromSlash(packagePath),
    )


====================
二、配置字段
====================

推荐将原来的：

package

改成：

packagePath

例如：

{
  "generation": {
    "packagePath": "ai/brawl"
  }
}

Go 配置结构：

type GenerationConfig struct {
    PackagePath string `json:"packagePath"`
}

不要再单独保存 packageName。

packageName 每次根据 PackagePath 自动计算：

packageName := path.Base(packagePath)


如果为了兼容旧工程，旧 JSON 里可能仍然存在：

{
  "generation": {
    "package": "bt_brawl"
  }
}

则需要兼容读取。

建议兼容逻辑：

1. 优先读取 packagePath
2. 如果 packagePath 为空，则读取旧 package
3. 保存时统一保存 packagePath
4. 老工程打开并再次保存后自动升级成新格式

例如：

func (c GenerationConfig) EffectivePackagePath() string {
    if c.PackagePath != "" {
        return c.PackagePath
    }
    return c.Package
}

旧字段可标记 deprecated，但暂时保留反序列化兼容。


====================
三、统一使用 / 保存路径
====================

配置文件中的 packagePath 必须统一保存为：

ai/brawl

不能保存成：

ai\brawl

即：

配置层统一使用 `/`

真正访问文件系统时再：

filepath.FromSlash(packagePath)

Windows：

ai/brawl

最终转换为：

ai\brawl

Linux：

ai/brawl

保持不变。


====================
四、删除 generated 固定目录
====================

搜索整个项目，删除所有类似：

filepath.Join(projectDir, "generated", packageName)

或者：

generated/<package>

的固定逻辑。

统一改成：

packagePath := generation.PackagePath

packageName := path.Base(packagePath)

outputDir := filepath.Join(
    projectDir,
    filepath.FromSlash(packagePath),
)

也就是说：

packagePath 本身就是相对于工程根目录的最终输出目录。

不要再自动追加：

generated

也不要再自动追加：

packageName


====================
五、UI 修改
====================

当前右侧配置如果叫：

包名

改成：

生成包路径

输入框示例：

ai/brawl

说明文字改成：

生成目录相对于当前工程目录。
路径最后一级目录名将作为 Go package 名。

例如：

ai/brawl

生成到：

<工程目录>/ai/brawl/

Go package：

package brawl


建议输入框 placeholder：

例如：ai/brawl


如果当前 UI 仍然显示：

包名

必须修改，避免用户误以为这里只能填写：

brawl

因为现在允许：

ai/brawl


====================
六、UI 实时提示
====================

建议在输入框下方实时显示解析结果。

例如用户输入：

ai/brawl

显示：

生成目录：
ai/brawl

Go package：
brawl


用户输入：

bt_brawl

显示：

生成目录：
bt_brawl

Go package：
bt_brawl


这样可以让规则一眼可见。


====================
七、合法性校验
====================

PackagePath 必须做严格校验。

允许：

bt_brawl
ai/brawl
game/ai/npc
server/behavior/brawl

禁止：

空字符串

/
./brawl
../brawl
ai/../brawl
ai/./brawl
ai//brawl

绝对路径：

C:/xxx
C:\xxx
/var/xxx

以及任何可以逃出工程目录的路径。


必须防止目录穿越：

../
..\


最终 outputDir 必须保证仍然位于 projectDir 内。


====================
八、每一级目录名校验
====================

packagePath：

game/ai/brawl

需要分别检查：

game
ai
brawl

不能出现非法目录名。

特别是最后一级：

brawl

必须是合法 Go package 标识符。


例如允许：

brawl
bt_brawl
npc
ai2

禁止：

bt-brawl
123brawl
brawl.test
brawl test


可使用 Go token / scanner 相关能力校验 identifier。

至少满足：

^[A-Za-z_][A-Za-z0-9_]*$


并避免 Go keyword，例如：

type
func
package
var
const
map
range

这些不能作为最终 package 名。


====================
九、package 生成规则
====================

任何地方需要生成：

package xxx

必须统一使用：

packageName := path.Base(packagePath)

不能直接：

package packagePath

例如：

packagePath:

ai/brawl

错误：

package ai/brawl

正确：

package brawl


====================
十、目录与 package 一致性
====================

设计原则：

目录最后一级名称 == packageName

因此不允许出现：

目录：

ai/brawl

但配置：

package bt_brawl

这种状态。

不要再设计独立的：

OutputDir
PackageName

两个可自由配置字段。

统一由：

PackagePath

决定。


====================
十一、CLI 行为同步
====================

检查当前 bttool generate CLI。

如果目前存在：

--out

则需要统一语义。

推荐最终 CLI：

bttool generate \
  --file brawl-bt.json \
  --out ai/brawl

含义：

--out 是相对于当前工程目录的“生成包路径”。

最终：

./ai/brawl/

package：

brawl


如果 CLI 当前还要求额外 package 参数：

--package

则考虑移除。

packageName 必须从：

path.Base(out)

自动推导。


如果出于兼容性暂时保留 --package：

1. 标记 deprecated
2. 如果填写 --package，则必须与 path.Base(--out) 相同
3. 不相同直接报错

例如：

--out ai/brawl
--package npc

必须报错：

package name "npc" does not match output directory package "brawl"


最终目标是删除独立 package 配置。


====================
十二、生成目录自动创建
====================

如果：

packagePath = ai/brawl

而：

ai/
brawl/

均不存在，则自动：

MkdirAll(outputDir)

最终自动创建完整目录。


====================
十三、已有目录 package 冲突检查
====================

生成前检查目标目录已有的 .go 文件。

例如：

ai/brawl/helper.go

内容：

package npc

但当前 PackagePath：

ai/brawl

推导 package：

brawl

此时必须阻止生成。

报错类似：

目标目录中已存在 Go package "npc"，
但当前生成包名为 "brawl"。

请调整生成包路径或已有源码的 package。


注意：

忽略：

*_test.go

是否单独处理可以根据现有逻辑判断，但普通 .go 文件必须保证 package 一致。


====================
十四、生成文件清理安全
====================

非常重要：

以后生成目录可能直接是业务源码目录，例如：

internal/ai/brawl/

其中可能同时存在：

actions.go
helper.go
service.go
glue.gen.go
tree_npc.gen.go
tree_gen.map.json

绝对不能因为重新生成而：

os.RemoveAll(outputDir)

也不能遍历删除所有：

*.go

只能删除生成器自己管理的文件。


继续使用：

tree_gen.map.json

或现有生成清单记录自动生成文件。


例如旧清单记录：

glue.gen.go
tree_old.gen.go

新生成只需要：

glue.gen.go
tree_new.gen.go

则可以删除：

tree_old.gen.go

但绝不能删除：

actions.go
helper.go
service.go


原则：

只删除明确记录为“生成器拥有”的文件。


====================
十五、生成文件标记
====================

建议所有自动生成的 Go 文件顶部统一包含：

// Code generated by behaviortree. DO NOT EDIT.

例如：

// Code generated by behaviortree. DO NOT EDIT.

package brawl

便于开发者快速识别自动生成代码。


====================
十六、示例工程最终结构
====================

当前：

behaviortree_test/
├─ context/
├─ generated/
│  └─ bt_brawl/
├─ main.go
├─ brawl-bt.json
├─ go.mod
└─ go.sum

修改后，如果配置：

packagePath = brawl

最终：

behaviortree_test/
├─ context/
├─ brawl/
│  ├─ actions.go
│  ├─ glue.gen.go
│  ├─ tree_npc_troop.gen.go
│  └─ tree_gen.map.json
├─ main.go
├─ brawl-bt.json
├─ go.mod
└─ go.sum


如果配置：

packagePath = ai/brawl

最终：

behaviortree_test/
├─ context/
├─ ai/
│  └─ brawl/
│     ├─ actions.go
│     ├─ glue.gen.go
│     ├─ tree_npc_troop.gen.go
│     └─ tree_gen.map.json
├─ main.go
├─ brawl-bt.json
├─ go.mod
└─ go.sum


其中所有 ai/brawl/*.go：

package brawl


====================
十七、工程文件移动后的处理
====================

如果用户之前配置：

bt_brawl

后来修改为：

ai/brawl

不要自动删除整个旧目录：

bt_brawl/

因为其中可能存在用户手写代码。

可以：

1. 在新目录正常生成
2. 对旧目录，只根据旧 tree_gen.map.json 清理“生成器拥有”的旧生成文件
3. 保留所有用户手写文件
4. 如果清理后旧目录为空，可以安全删除空目录
5. 如果仍存在手写文件，则保留目录


====================
十八、路径修改后的提示
====================

如果 UI 检测到 PackagePath 从：

bt_brawl

改为：

ai/brawl

保存或生成时可以提示：

生成包路径已变更：

bt_brawl
→
ai/brawl

新代码将生成到：

ai/brawl

旧目录中的自动生成文件将按生成清单安全清理，
手写文件不会删除。


====================
十九、生成预览
====================

如果当前有：

预览 Go

功能，也必须使用相同规则。

packagePath：

ai/brawl

预览必须显示：

package brawl

不能显示：

package ai/brawl

也不能显示旧配置 package。


====================
二十、生成按钮行为
====================

点击：

生成到目录

时：

1. 读取 PackagePath
2. normalize 为 `/`
3. 校验 PackagePath
4. 获取 packageName = path.Base(PackagePath)
5. 获取 outputDir
6. 确认 outputDir 未逃出 projectDir
7. 检查现有 Go package 冲突
8. 创建目录
9. 根据旧生成清单安全清理废弃生成文件
10. 生成新的 .gen.go
11. 写新的 tree_gen.map.json
12. UI 刷新工程文件列表


====================
二十一、建议提供统一辅助函数
====================

不要让 Web、CLI、Preview、Generator 分别自己解析。

新增统一函数，例如：

type ResolvedGoPackage struct {
    PackagePath string
    PackageName string
    OutputDir   string
}

func ResolveGoPackage(
    projectDir string,
    packagePath string,
) (ResolvedGoPackage, error)

统一负责：

- normalize
- validate
- packageName
- outputDir
- 防目录穿越


伪代码：

func ResolveGoPackage(projectDir, raw string) (ResolvedGoPackage, error) {
    packagePath := strings.TrimSpace(raw)
    packagePath = strings.ReplaceAll(packagePath, "\\", "/")

    // 校验空路径、绝对路径、.、..、非法分段等

    packagePath = path.Clean(packagePath)

    if packagePath == "." ||
       packagePath == "" ||
       strings.HasPrefix(packagePath, "../") {
        return ..., err
    }

    packageName := path.Base(packagePath)

    // 校验 packageName 是合法 Go identifier

    outputDir := filepath.Join(
        projectDir,
        filepath.FromSlash(packagePath),
    )

    // 再校验 outputDir 确实位于 projectDir 内

    return ResolvedGoPackage{
        PackagePath: packagePath,
        PackageName: packageName,
        OutputDir:   outputDir,
    }, nil
}


Web、CLI、预览、真正生成代码全部调用这一套。


====================
二十二、测试要求
====================

补充单元测试。


Case 1：

packagePath:

brawl

结果：

PackageName = brawl

OutputDir:

<project>/brawl


Case 2：

packagePath:

ai/brawl

结果：

PackageName = brawl

OutputDir:

<project>/ai/brawl


Case 3：

packagePath:

game/ai/npc

结果：

PackageName = npc


Case 4：

packagePath:

../brawl

必须失败。


Case 5：

packagePath:

ai/../brawl

必须失败，不要仅 Clean 后接受。


Case 6：

packagePath:

C:/temp/brawl

必须失败。


Case 7：

packagePath:

/tmp/brawl

必须失败。


Case 8：

packagePath:

ai/bt-brawl

必须失败，因为最终 Go package 名非法。


Case 9：

packagePath:

ai/type

必须失败，因为最终 package 是 Go keyword。


Case 10：

目录：

ai/brawl

已有：

helper.go

内容：

package brawl

允许继续生成。


Case 11：

目录：

ai/brawl

已有：

helper.go

内容：

package npc

必须阻止生成。


Case 12：

目录内：

actions.go
glue.gen.go
tree_old.gen.go

重新生成后：

actions.go 必须保留。


====================
二十三、最终设计原则
====================

最终只保留一个核心概念：

PackagePath

例如：

ai/brawl

它同时代表：

生成目录：

<project>/ai/brawl

Go package：

brawl


必须始终满足：

path.Base(PackagePath) == Go package name


不要再固定生成：

generated/<package>

不要再允许：

outputDir

和：

packageName

独立配置造成不一致。


====================
二十四、最终 UI 示例
====================

Go 生成设置

生成包路径
┌─────────────────────────┐
│ ai/brawl                │
└─────────────────────────┘

生成目录：ai/brawl
Go package：brawl

说明：

生成包路径相对于当前工程目录。
路径最后一级目录名将作为 Go package 名。
例如 ai/brawl 会生成到 ai/brawl/，package 为 brawl。


业务上下文导入路径
┌─────────────────────────┐
│ bt_test/context         │
└─────────────────────────┘

上下文类型
┌─────────────────────────┐
│ *Context                │
└─────────────────────────┘


====================
二十五、验收标准
====================

完成修改后必须满足：

1. 不再自动创建 generated 目录。
2. packagePath=brawl 时直接生成到 ./brawl。
3. packagePath=ai/brawl 时生成到 ./ai/brawl。
4. ai/brawl 下生成的 Go 文件全部为 package brawl。
5. 目录最后一级始终等于 Go package。
6. Web、CLI、Go 预览使用完全相同的解析规则。
7. Windows/Linux 都能正常使用。
8. JSON 中路径统一保存为 `/`。
9. 防止 ../ 等路径逃逸。
10. 不会误删用户手写 Go 文件。
11. 兼容旧 package 配置并能自动迁移到 packagePath。
12. 补齐相关测试。
13. 修改 README / 示例文档中的旧 generated/<package> 说明。
14. 修改现有示例工程，使其符合新的目录规则。
15. 执行现有测试、新增测试、go test ./...，确保全部通过。

请先检查当前仓库实际代码结构和现有实现，然后直接完成以上改造；尽量复用现有逻辑，不做无关重构。