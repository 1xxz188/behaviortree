package behaviortree

import (
	"context"
	"log/slog"
)

// defaultLog 在未配置接收者时输出结构化生命周期日志；节点追踪仍由 Options.Trace 控制。
func defaultLog(record LogRecord) {
	level := slog.LevelInfo
	if record.Kind == LogError {
		level = slog.LevelError
	}
	attrs := [11]slog.Attr{
		slog.String("component", "behaviortree"),
		slog.String("kind", record.Kind.String()),
		slog.String("version", record.Version),
		slog.String("instance", record.InstanceID),
		slog.String("tree", record.TreeID),
		slog.Uint64("sequence", record.Sequence),
	}
	count := 6
	if record.NodeID != "" {
		attrs[count], attrs[count+1] = slog.String("node", record.NodeID), slog.Int("node_index", record.NodeIndex)
		attrs[count+2] = slog.String("node_tree", record.NodeTreeID)
		count += 3
	}
	if record.Kind == LogNode {
		attrs[count], attrs[count+1] = slog.String("from", record.From.String()), slog.String("to", record.To.String())
		count += 2
	} else if record.Reason != "" {
		attrs[count] = slog.String("reason", record.Reason)
		count++
	}
	slog.Default().LogAttrs(context.Background(), level, "behavior tree", attrs[:count]...)
}
