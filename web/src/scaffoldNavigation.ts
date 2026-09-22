import type { Project } from "./project.ts";
import { createSourceIndex, sourceNodeKey } from "./generation.ts";
import type { SourceIndex, SourceLocation } from "./generation.ts";

// 骨架响应到达时建立业务函数和节点索引；后续选择节点只做 O(1) 查找。
export function createScaffoldSourceIndex(source: string, project: Project): SourceIndex {
  const functions = new Map<string, number>();
  source.split("\n").forEach((text, offset) => {
    const declaration = /^func ([^\s(]+)\(/.exec(text);
    if (declaration) functions.set(declaration[1]!, offset + 1);
  });
  const definitions = new Map(project.catalog.map(definition => [definition.id, definition]));
  const representatives = new Map<number, SourceLocation>();
  const byNode = new Map<string, SourceLocation[]>();
  for (const tree of project.trees) {
    for (const node of tree.nodes) {
      if (node.type !== "action" && node.type !== "condition") continue;
      const definition = definitions.get(node.binding ?? "");
      if (!definition || definition.kind !== node.type) continue;
      const line = functions.get(definition.goName);
      if (line === undefined) continue;
      const location: SourceLocation = {
        file: "actions.go", treeId: tree.id, nodeId: node.id,
        index: line, line, functionName: definition.goName,
      };
      byNode.set(sourceNodeKey(tree.id, node.id), [location]);
      // 共用函数的节点共享高亮标识；行号回跳默认选择首个引用节点。
      if (!representatives.has(line)) representatives.set(line, location);
    }
  }
  const index = createSourceIndex(source, [...representatives.values()]);
  index.byNode = byNode;
  return index;
}

// 仅遍历所选函数的行，包含完整函数体但不带入相邻声明或空白。
export function selectedScaffoldCode(index: SourceIndex, location: SourceLocation): string {
  let end = location.line;
  while (index.byLine.get(end)?.index === location.index) end++;
  return index.lines.slice(location.line - 1, end - 1).join("\n");
}
