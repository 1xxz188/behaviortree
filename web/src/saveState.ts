// 保存状态与请求修订号独立；修订号继续负责淘汰过期请求。
export class ProjectSaveState {
  dirty = false; // 当前内容是否存在尚未写入文件的修改。
  private baseline?: string; // 最近成功打开或保存的无损 JSON 快照。

  // 打开文件设置基准；新建和导入尚未落盘，因此没有已保存基准。
  reset(savedSnapshot?: string): void {
    this.baseline = savedSnapshot;
    this.dirty = savedSnapshot === undefined;
  }

  // 普通编辑只更新标记，不逐次输入序列化工程。
  changed(): void {
    this.dirty = true;
  }

  // 历史恢复复用已有 JSON 快照，不额外遍历工程对象。
  restore(snapshot: string): void {
    this.dirty = this.baseline === undefined || snapshot !== this.baseline;
  }

  // 成功响应只确认发送时的快照，保存期间后续编辑仍然未保存。
  saved(snapshot: string, currentSnapshot: string): void {
    this.baseline = snapshot;
    this.dirty = currentSnapshot !== snapshot;
  }
}
