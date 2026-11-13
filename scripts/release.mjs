#!/usr/bin/env node
// 发版脚本（单模块纯库项目，对齐 license-cloud release.mjs --target sdk 模式）：
//   门禁（gofmt → build → vet → test）→ 发版必要性探测 → 创建 vX.Y.Z 轻量 tag（默认不推）。
// tag 推送即发布：goproxy 按语义化 tag 自动缓存，无需任何构建产物。
//
// 用法：
//   node scripts/release.mjs --check            # 只跑门禁，不探测、不建 tag
//   node scripts/release.mjs 0.1.0              # 门禁 → 探测 → 建 v0.1.0 tag（不推送）
//   node scripts/release.mjs v0.1.0 --push      # 建 tag 并推送 origin（推送即发布）
//   node scripts/release.mjs --skip-test 0.1.0  # 跳过 go test（其余门禁保留）
//   node scripts/release.mjs 0.1.0 --resume     # tag 已存在时复用（只补推）
//   node scripts/release.mjs --force 0.1.0      # 最新 tag 与 HEAD 无变更时仍强制发版
//
// 版本号：v?X.Y.Z 语义化，允许预发布 -alpha/-beta/-rc.N；tag 统一规范化为 v 前缀。
// 依赖：Node ≥18、git、go；不安装任何第三方包。git 写操作（建 tag/推送）仅由
// 显式调用本脚本的用户触发，脚本不做 commit/改文件等其它写操作。

import { spawnSync } from 'node:child_process';
import { createInterface } from 'node:readline';
import { existsSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const SEMVER_RE = /^v?(\d+)\.(\d+)\.(\d+)(?:-(alpha|beta|rc)(?:\.(\d+))?)?$/;

/**
 * run 执行外部命令并继承 stdio（门禁需实时输出）或捕获输出。
 *
 * @param {string} cmd 命令名（依赖 PATH 解析，跨平台无需 shell）
 * @param {string[]} args 参数数组
 * @param {{capture?: boolean, cwd?: string}} [opts] capture 为 true 时捕获 stdout
 * @returns {{ok: boolean, stdout: string, stderr: string}} ok 为退出码是否为 0
 * @throws 不抛出异常，全部以返回值表达
 */
function run(cmd, args, opts = {}) {
  const res = spawnSync(cmd, args, {
    cwd: opts.cwd ?? repoRoot,
    encoding: 'utf8',
    stdio: opts.capture ? ['ignore', 'pipe', 'pipe'] : 'inherit',
    shell: false,
  });
  return {
    ok: res.status === 0,
    stdout: (res.stdout ?? '').trim(),
    stderr: (res.stderr ?? '').trim(),
  };
}

/**
 * die 输出错误并以非零码退出。
 *
 * @param {string} msg 错误描述（单行或带缩进的多行）
 * @returns {never} 永不返回
 */
function die(msg) {
  console.error(`\n✗ ${msg}`);
  process.exit(1);
}

/**
 * parseArgs 解析命令行参数。
 *
 * @param {string[]} argv process.argv.slice(2)
 * @returns {{check: boolean, push: boolean, force: boolean, skipTest: boolean, resume: boolean, version: string}} 解析结果；version 为原始版本串（可含 v 前缀），缺省空串
 * @throws 未知参数时直接 die 退出
 */
function parseArgs(argv) {
  const opts = { check: false, push: false, force: false, skipTest: false, resume: false, version: '' };
  for (const arg of argv) {
    switch (arg) {
      case '--check': opts.check = true; break;
      case '--push': opts.push = true; break;
      case '--force': opts.force = true; break;
      case '--skip-test': opts.skipTest = true; break;
      case '--resume': opts.resume = true; break;
      case '-h': case '--help': printHelp(); process.exit(0);
      default:
        if (arg.startsWith('--')) die(`未知参数 ${arg}（--help 查看用法）`);
        if (opts.version) die(`版本号重复：${opts.version} 与 ${arg}`);
        opts.version = arg;
    }
  }
  return opts;
}

/** printHelp 输出用法说明后由调用方退出。参数与返回值均无，异常仅包括 stdout 写失败。 */
function printHelp() {
  console.log(`用法：node scripts/release.mjs [--check|--push|--force|--skip-test|--resume] [版本号]

  --check      只跑门禁（gofmt → build → vet → test），不探测、不建 tag
  --push       建 tag 后推送到 origin（推送即发布；缺省只建本地 tag）
  --force      最新 tag 与 HEAD 无变更时仍强制发版
  --skip-test  跳过 go test（gofmt/build/vet 保留）
  --resume     tag 已存在时复用（配合 --push 只补推）
  版本号       v?X.Y.Z，允许 -alpha/-beta/-rc.N 预发布`);
}

/**
 * normalizeTag 版本号规范化为 v 前缀 tag 名。
 *
 * @param {string} input 用户输入的版本号（v?X.Y.Z[-pre[.N]]）
 * @returns {string} 规范化后的 tag 名，如 "v0.1.0"、"v1.2.0-rc.1"
 * @throws 格式非法时直接 die 退出
 */
function normalizeTag(input) {
  const m = SEMVER_RE.exec(input.trim());
  if (!m) die(`非法版本号 "${input}"：期望 v?X.Y.Z，预发布支持 -alpha/-beta/-rc.N`);
  const [, maj, min, pat, pre, preN] = m;
  return `v${maj}.${min}.${pat}${pre ? `-${pre}${preN ? `.${preN}` : ''}` : ''}`;
}

/**
 * latestTag 返回按创建时间倒序的最新 v* tag；无 tag 时返回空串。
 *
 * @returns {string} 最新 tag 名（如 "v0.1.0"），仓库无 tag 时为 ""
 * @throws git 不可用时 die
 */
function latestTag() {
  const res = run('git', ['tag', '--sort=-creatordate', '--list', 'v*'], { capture: true });
  if (!res.ok) die(`git tag 失败：${res.stderr}`);
  return res.stdout.split('\n')[0] ?? '';
}

/**
 * bumpVersion 基于最新 tag 生成候选版本（patch/minor/major 递增）。
 *
 * @param {string} tag 最新 tag（如 "v0.2.3"），空串时以 v0.0.0 起步
 * @returns {string[]} 三个候选版本号（含 v 前缀），如 ["v0.2.4","v0.3.0","v1.0.0"]
 * @throws 最新 tag 非 semver 时按 v0.0.0 兜底，不抛出
 */
function bumpCandidates(tag) {
  const m = SEMVER_RE.exec(tag) ?? [null, '0', '0', '0'];
  const [maj, min, pat] = [Number(m[1]), Number(m[2]), Number(m[3])];
  return [`v${maj}.${min}.${pat + 1}`, `v${maj}.${min + 1}.0`, `v${maj + 1}.0.0`];
}

/**
 * pickVersion 交互选择候选版本（↑/↓ + Enter），非 TTY 且无显式版本号时直接退出。
 *
 * @param {string[]} candidates 候选版本列表
 * @param {string} explicit 用户显式传入的版本号，非空时跳过交互
 * @returns {Promise<string>} 选定的版本号
 * @throws 用户中断（Ctrl+C）时进程退出
 */
async function pickVersion(candidates, explicit) {
  if (explicit) return normalizeTag(explicit);
  if (!process.stdin.isTTY) {
    die(`请显式传入版本号，例如：node scripts/release.mjs ${candidates[0]}`);
  }
  let index = 0;
  const render = () => {
    console.log('\n选择要发布的版本（↑/↓ 移动，Enter 确认，Ctrl+C 取消）：');
    candidates.forEach((v, i) => console.log(`${i === index ? '❯' : ' '} [${i + 1}] ${v}`));
    console.log(`\n（最新 tag: ${latestTag() || '无'}）`);
  };
  render();
  const rl = createInterface({ input: process.stdin, terminal: true });
  return new Promise((resolve) => {
    const onKey = (_ch, key) => {
      if (!key) return;
      if (key.name === 'up') { index = (index + candidates.length - 1) % candidates.length; }
      else if (key.name === 'down') { index = (index + 1) % candidates.length; }
      else if (key.name === 'c' && key.ctrl) { rl.close(); process.exit(130); }
      else if (key.name === 'return' || key.name === 'enter') {
        rl.close();
        console.log();
        resolve(candidates[index]);
        return;
      } else { return; }
      // 移动光标回退到列表首行重绘：候选仅 3 行，直接清屏重绘最简单可靠
      process.stdout.write('\x1B[1A\x1B[2K'.repeat(candidates.length + 4));
      render();
    };
    process.stdin.on('keypress', onKey);
    rl.on('close', () => { process.stdin.off('keypress', onKey); });
    process.stdin.setRawMode(true);
  });
}

// ---------- 门禁 ----------

/** gateGofmt gofmt 格式闸门：任何未格式化文件即失败。参数无；返回值无；发现未格式化文件时 die。 */
function gateGofmt() {
  console.log('▸ gofmt 格式检查');
  const res = run('gofmt', ['-l', '.'], { capture: true });
  if (!res.ok) die(`gofmt 执行失败：${res.stderr}`);
  if (res.stdout) die(`以下文件未通过 gofmt 格式检查：\n  ${res.stdout.split('\n').join('\n  ')}\n  修复：gofmt -w .`);
}

/** gateBuild 编译检查：go build ./...。参数无；返回值无；失败时 die。 */
function gateBuild() {
  console.log('▸ go build ./...');
  const res = run('go', ['build', './...']);
  if (!res.ok) die('编译失败');
}

/** gateVet 静态检查：go vet ./...。参数无；返回值无；失败时 die。 */
function gateVet() {
  console.log('▸ go vet ./...');
  const res = run('go', ['vet', './...']);
  if (!res.ok) die('go vet 未通过');
}

/** gateTest 单元测试：go test -race ./...。参数无；返回值无；失败时 die。 */
function gateTest() {
  console.log('▸ go test -race ./...');
  const res = run('go', ['test', '-race', './...']);
  if (!res.ok) die('单元测试未通过');
}

/**
 * runGates 顺序执行全部门禁。
 *
 * @param {boolean} skipTest 为 true 时跳过 go test
 * @returns {void} 任一门禁失败即 die 退出
 * @throws 不抛出异常
 */
function runGates(skipTest) {
  gateGofmt();
  gateBuild();
  gateVet();
  if (!skipTest) gateTest();
  console.log('✓ 门禁全部通过');
}

// ---------- 探测与 tag ----------

/**
 * assertCleanTree 要求工作区干净：tag 指向 HEAD，未提交改动不会进入发布。
 *
 * @returns {void} 存在未提交改动时 die
 * @throws 不抛出异常
 */
function assertCleanTree() {
  const res = run('git', ['status', '--porcelain'], { capture: true });
  if (!res.ok) die(`git status 失败：${res.stderr}`);
  if (res.stdout) {
    die(`工作区存在未提交改动，tag 只会指向 HEAD，先提交：\n  ${res.stdout.split('\n').join('\n  ')}`);
  }
}

/**
 * assertChangedSince 探测发版必要性：最新 tag 与 HEAD 之间必须有变更。
 *
 * @param {string} tag 最新 tag，空串表示首次发版（视为有变更）
 * @param {boolean} force 为 true 时跳过探测
 * @returns {void} 无变更且未 force 时 die
 * @throws 不抛出异常
 */
function assertChangedSince(tag, force) {
  if (!tag || force) return;
  const res = run('git', ['diff', '--stat', `${tag}..HEAD`], { capture: true });
  if (!res.ok) die(`git diff 失败：${res.stderr}`);
  if (!res.stdout) {
    die(`自 ${tag} 以来没有任何变更，无需发版（--force 可强制）`);
  }
  console.log(`▸ 自 ${tag} 以来的变更：\n  ${res.stdout.split('\n').join('\n  ')}`);
}

/**
 * createTag 创建轻量 tag；已存在时按 resume 语义复用或退出。
 *
 * @param {string} tag 规范化 tag 名
 * @param {boolean} resume 为 true 且 tag 已存在时复用（不重建）
 * @returns {boolean} 本次是否新建了 tag（false 表示复用已有 tag）
 * @throws tag 已存在且未 resume 时 die
 */
function createTag(tag, resume) {
  const exists = run('git', ['rev-parse', '-q', '--verify', `refs/tags/${tag}`], { capture: true }).ok;
  if (exists) {
    if (!resume) die(`tag ${tag} 已存在；确认无误后用 --resume 复用并（配合 --push）补推`);
    console.log(`▸ 复用已存在的 tag ${tag}`);
    return false;
  }
  const res = run('git', ['tag', tag]);
  if (!res.ok) die(`创建 tag 失败：${res.stderr}`);
  console.log(`✓ 已创建轻量 tag ${tag}（指向 HEAD）`);
  return true;
}

/**
 * pushTag 推送 tag 到 origin。
 *
 * @param {string} tag tag 名
 * @returns {void} 失败时 die（含网络/权限场景的手动补救提示）
 * @throws 不抛出异常
 */
function pushTag(tag) {
  console.log(`▸ 推送 ${tag} → origin`);
  const res = run('git', ['push', 'origin', tag]);
  if (!res.ok) die(`推送失败，可稍后手动执行：git push origin ${tag}`);
  console.log(`✓ 已推送。goproxy 缓存生效后即可 go get github.com/jcbowen/sms-go@${tag}`);
}

/**
 * main 主流程：解析参数 → 门禁 → （非 check）探测 → 选版本 → 建 tag →（按需）推送。
 *
 * @returns {Promise<void>} 各步骤失败均以 die 退出
 * @throws 不抛出异常
 */
async function main() {
  const opts = parseArgs(process.argv.slice(2));
  if (!existsSync(path.join(repoRoot, 'go.mod'))) die(`未找到 go.mod，请在仓库根运行（当前根：${repoRoot}）`);
  const goRes = run('go', ['version'], { capture: true });
  if (!goRes.ok) die('未检测到 go 工具链，请确认 go 在 PATH 中');

  runGates(opts.skipTest);
  if (opts.check) {
    console.log('\n--check 模式：只跑门禁，不建 tag。');
    return;
  }

  assertCleanTree();
  const prev = latestTag();
  assertChangedSince(prev, opts.force);
  const tag = await pickVersion(bumpCandidates(prev), opts.version);
  const created = createTag(tag, opts.resume);
  if (opts.push) {
    pushTag(tag);
  } else if (created) {
    console.log(`\n下一步：确认无误后推送发布 → node scripts/release.mjs ${tag.replace(/^v/, '')} --push --resume`);
  }
}

main().catch((err) => die(err?.stack ?? String(err)));
