// 构建发行页面时保留 npm 运行依赖的许可声明，产物随 go:embed 一起分发。
import { readFileSync, readdirSync, mkdirSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const lock = JSON.parse(readFileSync(join(root, "package-lock.json"), "utf8"));
const sections = ["Behavior tree editor — third-party notices\n"];

for (const [path, info] of Object.entries(lock.packages).sort()) {
  if (!path || info.dev) continue;
  const dir = join(root, path);
  const pkg = JSON.parse(readFileSync(join(dir, "package.json"), "utf8"));
  const licenses = readdirSync(dir).filter((name) => /^licen[cs]e(?:[.-].*)?$/i.test(name));
  sections.push(`\n${pkg.name} ${pkg.version}\nLicense: ${info.license ?? pkg.license ?? "see package"}\n`);
  for (const license of licenses.sort()) {
    sections.push(readFileSync(join(dir, license), "utf8"));
  }
}

mkdirSync(join(root, "public"), { recursive: true });
writeFileSync(join(root, "public", "third-party-notices.txt"), sections.join("\n"));
