// Package helper 是可热更辅助代码，发布时和动作一起进入版本私有命名空间。
package helper

// Marker 在集成构建中被改成每个版本的不同值，验证辅助包也实际更新。
const Marker = "baseline"

// Label 返回日志中的业务版本标记。
func Label() string { return Marker }
