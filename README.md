# RIME雾凇拼音字典敏感词屏蔽工具

IceMinus（冰减）是一个用于屏蔽Rime输入法的[雾凇拼音（iDvel/rime-ice）](https://github.com/iDvel/rime-ice)字典敏感词（NSFW）的命令行工具。

##  使用说明
### 安装
推荐下载预编译的二进制文件，或自行编译：

```bash
env GOOS=windows GOARCH=amd64 go build -o bin/iceminus_windows_amd64.exe .
``` 

### 使用说明
下载后运行iceminus，指定要处理的目录，以Windows为例：
```
./iceminus.exe --path C:\Users\USERNAME\AppData\Roaming\Rime\cn_dicts # USERNAME替换为你的用户名
```
程序将递归扫描该目录下的所有YAML文件，查找包含敏感词的行，并在行首添加注释符号 `# ` 以屏蔽这些条目。

**随后，请执行Rime的`重启算法服务`以应用更改。**


## 详细说明

### 命令行参数

```bash
./iceminus --path <目录路径> [--dry-run] [--sensitive <敏感词文件>]
```

- **参数说明**:
- **--path**: 必需。要rime-ice字典所在的文件目录。
- **--dry-run**: 可选。若指定，仅打印匹配的行和将要做的修改，不会更改文件。
- **--sensitive**: 可选。敏感词文件路径，默认使用了 [sensitive_words.txt](sensitive_words.txt)。

### 运行示例

```bash
# 对目录进行检测并实际注释匹配行
./iceminus --path ./dicts

# 仅打印将要注释的行（不修改文件）
./iceminus --path ./dicts --dry-run

# 使用自定义敏感词列表
./iceminus --path ./dicts --sensitive ./my_sensitive.txt
```

当程序检测到包含敏感词的行时，会在控制台打印匹配文件和行号，例如：

```
path/to/file.yaml:12 -> 密码, secret
```

并在非 `--dry-run` 模式下把该行写回为注释行（在行首插入 `# `）。

### 敏感词文件

- 默认敏感词文件为 [sensitive_words.txt](sensitive_words.txt)。每行一个敏感词，空行会被忽略。
- 当使用 `--sensitive` 指定路径时，会优先读取外部文件；若未指定且程序使用了内嵌资源（见源码中的 `//go:embed`），则会使用内嵌内容作为默认词表。






