#!/usr/bin/env node
// 发版脚本（单模块纯库项目，对齐 license-cloud release.mjs --target sdk 模式）：
//   门禁（gofmt → build → vet → test）→ 发版必要性探测 → 创建 vX.Y.Z 轻量 tag（默认不推）。
// tag 推送即发布：goproxy 按语义化 tag 自动缓存，无需任何构建产物。
//
// 版本选择（对齐 license-cloud）：
//   3 个方向（patch/minor/major）× 4 个阶段（正式/-alpha/-beta/-rc）= 12 个候选；
//   预发布不带序号（v0.4.3-rc，不是 v0.4.3-rc.1），拒绝前导零与未知后缀；
//   交互：TTY 走两步方向键菜单（选方向 → 选阶段），raw mode 不可用降级为编号输入
//   （1 / 1-alpha / 2-rc / 完整版本号）；非 TTY 必须显式传版本号，不再隐式取默认。
//
// 用法：
//   node scripts/release.mjs --check              # 只跑门禁，不探测、不建 tag
//   node scripts/release.mjs 0.1.0                # 门禁 → 探测 → 建 v0.1.0 tag（不推送）
//   node scripts/release.mjs v0.1.0 --push        # 建 tag 并推送 origin（推送即发布）
//   node scripts/release.mjs 1-alpha              # 方向写法：1=patch，2=minor，3=major
//   node scripts/release.mjs --skip-test 0.1.0    # 跳过 go test（其余门禁保留）
//   node scripts/release.mjs 0.1.0 --resume       # tag 已存在时复用（只补推）
//   node scripts/release.mjs --force 0.1.0        # 最新 tag 与 HEAD 无变更时仍强制发版
//
// 依赖：Node ≥18、git、go；不安装任何第三方包。git 写操作（建 tag/推送）仅由
// 显式调用本脚本的用户触发，脚本不做 commit/改文件等其它写操作。

import { spawnSync } from 'node:child_process';
import { createInterface } from 'node:readline';
import { existsSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
// 预发布阶段白名单：alpha / beta / rc，仅表示阶段，不带序号（对齐 license-cloud）
const PRERELEASE_LABELS = ['alpha', 'beta', 'rc'];
// 用户输入的完整版本号（严格：拒绝前导零与 -rc.1 这类序号化预发布）
const STRICT_VERSION_RE = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-(alpha|beta|rc))?$/;

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
 * @returns {{check: boolean, push: boolean, force: boolean, skipTest: boolean, resume: boolean, version: string}} 解析结果；version 为原始版本串（可含 v 前缀或方向写法 1/2-rc），缺省空串
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

/** printHelp 输出用法说明后由调用方退出。参数与返回值均无；异常仅包括 stdout 写失败。 */
function printHelp() {
  console.log(`用法：node scripts/release.mjs [--check|--push|--force|--skip-test|--resume] [版本号]

  --check      只跑门禁（gofmt → build → vet → test），不探测、不建 tag
  --push       建 tag 后推送到 origin（推送即发布；缺省只建本地 tag）
  --force      最新 tag 与 HEAD 无变更时仍强制发版
  --skip-test  跳过 go test（gofmt/build/vet 保留）
  --resume     tag 已存在时复用（配合 --push 只补推）
  版本号       v?X.Y.Z（正式版）或 vX.Y.Z-alpha/-beta/-rc（预发布，不带序号）；
               也支持方向写法 1 / 2-rc（1-3 = patch/minor/major）；
               省略则交互选择（非 TTY 必须显式传入）`);
}

/**
 * normalizeVersion 版本号规范化为 v 前缀。
 *
 * @param {string} raw 用户输入的版本串（v 前缀可省略）
 * @returns {string} 规范化后的版本串；空输入返回空串
 * @throws 不抛出异常
 */
function normalizeVersion(raw) {
  const text = String(raw ?? '').trim();
  if (!text) return '';
  return text.startsWith('v') ? text : `v${text}`;
}

/**
 * validateVersion 校验完整版本号（严格格式）。
 *
 * @param {string} version 规范化后的版本串（v 前缀）
 * @returns {string} 校验失败原因；合法返回空串
 * @throws 不抛出异常
 * @example
 * validateVersion('v0.4.3-rc.1'); // '预发布已取消序号：…'（不再接受 .1 序号）
 * validateVersion('v0.04.3');     // 格式不合法（前导零）
 */
function validateVersion(version) {
  if (/-[A-Za-z]+\.\d+$/.test(version)) {
    return '预发布已取消序号：请写 v0.4.3-alpha / -beta / -rc（不要再带 .1）';
  }
  if (!STRICT_VERSION_RE.test(version)) {
    return `版本号格式不合法 "${version}"：需形如 v0.4.3 或 v0.4.3-alpha/-beta/-rc（不接受前导零或未知后缀）`;
  }
  return '';
}

/**
 * parseVersionTag 解析版本 tag 的语义化结构。
 *
 * @param {string} tag 形如 v0.4.4 或 v0.4.4-rc
 * @returns {{tag: string, core: string, major: number, minor: number, patch: number, stage: string}|null} 解析结果；非法返回 null
 * @throws 不抛出异常
 * @example
 * parseVersionTag('v0.4.4-rc'); // { core: '0.4.4', stage: 'rc', ... }
 */
function parseVersionTag(tag) {
  const m = /^v(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$/.exec(tag);
  if (!m) return null;
  return {
    tag,
    core: `${Number(m[1])}.${Number(m[2])}.${Number(m[3])}`,
    major: Number(m[1]),
    minor: Number(m[2]),
    patch: Number(m[3]),
    stage: m[4] ?? '',
  };
}

/**
 * versionRank 预发布阶段权重：alpha < beta < rc < 正式版；未知后缀按最低权重处理。
 *
 * @param {string} stage 阶段标识（空串表示正式版）
 * @returns {number} 权重值（越大越新）
 * @throws 不抛出异常
 * @example
 * versionRank('rc'); // 3
 */
function versionRank(stage) {
  if (!stage) return 4;
  const index = PRERELEASE_LABELS.indexOf(stage);
  return index >= 0 ? index + 1 : 0;
}

/**
 * compareVersion 纯 JS 版本比较（替代 git version sort，跨平台/跨机器结果一致）。
 *
 * 先比较 major/minor/patch，再比较阶段权重；同 core 下预发布排在正式版之前。
 *
 * @param {{major: number, minor: number, patch: number, stage: string}} a parseVersionTag 的结果
 * @param {{major: number, minor: number, patch: number, stage: string}} b parseVersionTag 的结果
 * @returns {number} 负数=a 更旧，正数=a 更新，0=相同
 * @throws 不抛出异常
 * @example
 * compareVersion(parseVersionTag('v0.4.3-rc'), parseVersionTag('v0.4.4')); // -1
 */
function compareVersion(a, b) {
  const core = (a.major - b.major) || (a.minor - b.minor) || (a.patch - b.patch);
  if (core !== 0) return core;
  return versionRank(a.stage) - versionRank(b.stage);
}

/**
 * listAllTags 列出本地全部 v* 版本 tag（无排序保证，排序交给 compareVersion）。
 *
 * @returns {string[]} tag 列表；无 tag 时为空数组
 * @throws git 不可用时 die
 */
function listAllTags() {
  const res = run('git', ['tag', '--list', 'v*'], { capture: true });
  if (!res.ok) die(`git tag 失败：${res.stderr}`);
  return res.stdout ? res.stdout.split('\n').map((s) => s.trim()).filter(Boolean) : [];
}

/**
 * latestTag 返回语义最高的 v* tag；无 tag 时返回空串。
 *
 * 不用 git --sort=-creatordate：git 的版本排序会把同 core 的预发布排在正式版
 * 之前，且受本机 versionsort.suffix 配置影响；改用 compareVersion 纯 JS 比较。
 *
 * @returns {string} 最新 tag 名（如 "v0.1.0"、"v0.2.0-rc"），仓库无合法 tag 时为 ""
 * @throws git 不可用时 die
 */
function latestTag() {
  const highest = listAllTags()
    .map(parseVersionTag)
    .filter(Boolean)
    .reduce((max, cur) => (!max || compareVersion(cur, max) > 0 ? cur : max), null);
  return highest ? highest.tag : '';
}

/**
 * bumpCandidates 基于最新 tag 生成三个变更方向的候选。
 *
 * 涨哪一位由变更内容决定：修 bug → patch，新增功能 → minor，不兼容/大改 → major。
 * 候选一律基于最新 tag 的主版本号（预发布 tag 的阶段后缀不参与计算），
 * 预发布不是独立候选，而是给选中方向附加阶段标记（-alpha/-beta/-rc）。
 *
 * @param {string} tag 最新 tag（如 "v0.2.3" 或 "v0.2.3-rc"），空串表示首次发版
 * @returns {Array<{version: string, kind: string, label: string}>} 三个方向的候选
 * @throws 最新 tag 非 semver 时按 v0.0.0 兜底，不抛出
 * @example
 * bumpCandidates('v0.2.3-rc').map((c) => c.version); // ['v0.2.4', 'v0.3.0', 'v1.0.0']
 */
function bumpCandidates(tag) {
  if (!tag) {
    return [
      { version: 'v0.1.0', kind: 'patch', label: '首个版本（修 bug 级别）' },
      { version: 'v0.2.0', kind: 'minor', label: '首个版本（minor 起点）' },
      { version: 'v1.0.0', kind: 'major', label: '首个版本（major 起点）' },
    ];
  }
  const m = parseVersionTag(tag) ?? { major: 0, minor: 0, patch: 0 };
  return [
    { version: `v${m.major}.${m.minor}.${m.patch + 1}`, kind: 'patch', label: `修 bug（基于 ${tag}，patch +1）` },
    { version: `v${m.major}.${m.minor + 1}.0`, kind: 'minor', label: `新增功能（基于 ${tag}，minor +1）` },
    { version: `v${m.major + 1}.0.0`, kind: 'major', label: `不兼容/大改（基于 ${tag}，major +1）` },
  ];
}

/**
 * sameCoreTags 返回与给定版本共用同一主版本号（X.Y.Z）的全部已存在 tag。
 *
 * @param {string[]} allTags 本地全部 v* tag
 * @param {string} version 目标版本号
 * @returns {string[]} 同 core 的 tag 列表（可能为空）
 * @throws 目标版本号非法时按无同 core 处理（返回空数组），不抛出
 */
function sameCoreTags(allTags, version) {
  const target = parseVersionTag(version);
  if (!target) return [];
  return allTags.filter((t) => {
    const p = parseVersionTag(t);
    return p && p.core === target.core;
  });
}

/**
 * buildCandidateCatalog 生成完整候选清单：三个变更方向 × 四个阶段（正式/alpha/beta/rc）。
 *
 * bumpCandidates 只给三个方向的正式版；这里把全部 12 个可选版本连同占用状态
 * 一起列出，供交互展示与非 TTY 报错提示使用。
 *
 * @param {string[]} allTags 本地全部 v* tag
 * @param {Array<{version: string, kind: string, label: string}>} directions 三方向候选
 * @returns {Array<{version: string, kind: string, label: string, versions: Array<{version: string, stage: string, exact: boolean, conflict: boolean}>}>}
 *   每个方向含四个阶段：exact=同名 tag 已存在（可 --resume 复用），conflict=同 core 其它 tag 已存在（冲突）
 * @throws 不抛出异常
 * @example
 * buildCandidateCatalog(['v0.1.0'], bumpCandidates('v0.1.0'))[0].versions[3].version; // 'v0.2.0-rc'
 */
function buildCandidateCatalog(allTags, directions) {
  return directions.map((direction) => ({
    ...direction,
    versions: ['', ...PRERELEASE_LABELS].map((stage) => {
      const version = stage ? `${direction.version}-${stage}` : direction.version;
      return {
        version,
        stage,
        exact: allTags.includes(version),
        conflict: sameCoreTags(allTags, version).some((t) => t !== version),
      };
    }),
  }));
}

/**
 * printCandidateCatalog 打印完整候选清单（3 个方向 × 4 个阶段 = 12 项）。
 *
 * 正式版行带方向序号与推荐标记，其后紧跟 alpha / beta / rc 三个阶段行，
 * 占用状态（已存在/同号冲突）逐项标注，并给出选择写法。
 *
 * @param {Array<Object>} catalog buildCandidateCatalog 的结果
 * @param {(line: string) => void} [log] 输出函数（默认 console.log；报错时传 console.error）
 * @returns {void}
 * @throws 不抛出异常
 */
function printCandidateCatalog(catalog, log = console.log) {
  const highest = latestTag();
  log(`  当前最高版本：${highest || '无（仓库暂无 v* tag）'}`);
  log('  版本候选（3 个方向 × 4 个阶段 = 12 项；预发布不带序号，只在版本号后加 -alpha/-beta/-rc）：');
  catalog.forEach((direction, i) => {
    const formal = direction.versions[0];
    const formalMark = formal.exact ? '  [已存在]' : formal.conflict ? '  [同号冲突]' : '';
    const recommend = i === 0 ? '  ← 默认推荐' : '';
    log(`    ${i + 1}) ${formal.version}  正式版  ${direction.label}${formalMark}${recommend}`);
    direction.versions.slice(1).forEach((v) => {
      const mark = v.exact ? '  [已存在]' : v.conflict ? '  [同号冲突]' : '';
      log(`       ${v.version}  ${v.stage.padEnd(5)} 预发布${mark}`);
    });
  });
  log(`  选择写法：1-3 取正式版；1-alpha、2-beta、3-rc 取预发布；也可直接输入完整版本号（如 ${catalog[0].versions[3].version}）`);
}

/**
 * interpretVersionAnswer 把用户输入解释为版本号：序号、序号-阶段、完整版本号。
 *
 * 序号 1..3 对应 patch/minor/major 三个方向；阶段用「序号-阶段」表示，
 * 例如 `2-rc` = 第 2 个方向的 rc 版。不支持只给 `-rc` 这类无基数写法，
 * 空输入也不构成选择（必须显式指定）。
 *
 * @param {string} answer 用户输入
 * @param {Array<{version: string}>} candidates 三个方向的候选
 * @returns {{version: string}|{error: string}} 选中版本，或无法识别的具体原因
 * @throws 不抛出异常
 * @example
 * interpretVersionAnswer('2-rc', candidates); // { version: 'v0.5.0-rc' }
 */
function interpretVersionAnswer(answer, candidates) {
  if (!answer) return { error: '未输入选择（直接回车不会自动发布）' };
  const m = /^(\d+)(?:-(alpha|beta|rc))?$/.exec(answer);
  if (m) {
    const idx = Number(m[1]);
    if (idx < 1 || idx > candidates.length) {
      return { error: `序号超出范围（有效范围 1-${candidates.length}）` };
    }
    const core = candidates[idx - 1].version;
    return { version: m[2] ? `${core}-${m[2]}` : core };
  }
  const version = normalizeVersion(answer);
  const reason = validateVersion(version);
  return reason ? { error: reason } : { version };
}

/**
 * canUseRawMode 判断当前终端能否使用方向键菜单（raw mode）。
 *
 * CI、管道、部分受限终端返回 false，调用方应降级为编号输入。
 *
 * @returns {boolean} stdin/stdout 均为 TTY 且支持 setRawMode 时为 true
 * @throws 不抛出异常
 */
function canUseRawMode() {
  return Boolean(process.stdin.isTTY && process.stdout.isTTY
    && typeof process.stdin.setRawMode === 'function');
}

/**
 * selectMenu 方向键单选菜单（自绘实现，无第三方依赖）。
 *
 * ↑/↓/j/k 移动、Enter 确认、Esc/q 取消；Ctrl+C 退出（码 130）。
 * 调用前应确认 canUseRawMode() 为 true。
 *
 * @param {string} title 菜单标题
 * @param {string[]} items 选项文本（单行展示，过长可能折行）
 * @param {{initial?: number}} [opts] 初始高亮项下标
 * @returns {Promise<number|null>} 选中项下标；取消返回 null
 * @throws 不抛出异常；Ctrl+C 时进程以 130 退出
 * @example
 * const idx = await selectMenu('第一步：选择变更方向', ['v0.2.4  修 bug', 'v0.3.0  新增功能']);
 */
function selectMenu(title, items, opts = {}) {
  let index = opts.initial ?? 0;
  const render = () => {
    console.log(`\n${title}（↑/↓/j/k 移动，Enter 确认，Esc/q 取消，Ctrl+C 退出）：`);
    items.forEach((t, i) => console.log(`${i === index ? '❯' : ' '} [${i + 1}] ${t}`));
  };
  render();
  const rl = createInterface({ input: process.stdin, terminal: true });
  return new Promise((resolve) => {
    const onKey = (_ch, key) => {
      if (!key) return;
      if (key.name === 'up' || key.name === 'k') { index = (index + items.length - 1) % items.length; }
      else if (key.name === 'down' || key.name === 'j') { index = (index + 1) % items.length; }
      else if (key.name === 'escape' || key.name === 'q') { rl.close(); resolve(null); return; }
      else if (key.name === 'c' && key.ctrl) { rl.close(); process.exit(130); }
      else if (key.name === 'return' || key.name === 'enter') { rl.close(); resolve(index); return; }
      else { return; }
      // 移动光标回退到列表首行重绘：清掉「空行 + 标题行 + 选项行」后重画
      process.stdout.write('\x1B[1A\x1B[2K'.repeat(items.length + 2));
      render();
    };
    process.stdin.on('keypress', onKey);
    rl.on('close', () => { process.stdin.off('keypress', onKey); });
    process.stdin.setRawMode(true);
  });
}

/**
 * acceptSelectedVersion 对交互选出的版本做占用校验后返回。
 *
 * @param {string} version 交互选中的版本号
 * @param {string[]} allTags 本地全部 v* tag
 * @returns {string} 校验通过的版本号
 * @throws 同名 tag 已存在时提示 --resume；同 core 其它 tag 已存在时提示换号，均 die
 */
function acceptSelectedVersion(version, allTags) {
  if (allTags.includes(version)) {
    die(`tag ${version} 已存在；确认无误后用 --resume 复用并（配合 --push）补推`);
  }
  const sameCore = sameCoreTags(allTags, version).filter((t) => t !== version);
  if (sameCore.length) {
    die(`${version} 与已存在 tag ${sameCore.join('、')} 共用同一版本号；同一版本号只能出现一次，请选择下一个版本号`);
  }
  console.log(`  已选择版本: ${version}`);
  return version;
}

/**
 * resolveExplicitVersion 解析显式传入的版本号：完整版本号或方向写法（1 / 2-rc）。
 *
 * @param {string} input 用户传入的版本号（v 前缀可省略）
 * @param {Array<{version: string}>} candidates 三个方向的候选
 * @returns {string} 规范化后的版本号
 * @throws 格式非法（含序号化预发布、前导零）或方向写法无法识别时 die
 */
function resolveExplicitVersion(input, candidates) {
  const raw = String(input).trim();
  if (/^\d+(?:-(alpha|beta|rc))?$/.test(raw)) {
    const picked = interpretVersionAnswer(raw, candidates);
    if (picked.error) die(`方向写法 "${raw}" 无法识别：${picked.error}`);
    console.log(`▸ 已按方向选择版本: ${picked.version}`);
    return picked.version;
  }
  const version = normalizeVersion(raw);
  const reason = validateVersion(version);
  if (reason) die(`非法版本号 "${input}"：${reason}`);
  return version;
}

/**
 * pickVersionByMenu 两步方向键菜单选版本：先选变更方向，再选版本类型。
 *
 * @param {Array<{version: string, kind: string, label: string}>} candidates 三个方向的候选
 * @param {string[]} allTags 本地全部 v* tag
 * @returns {Promise<string>} 选定的版本号
 * @throws Esc 取消以 0 退出；选中已存在/冲突版本时 die
 */
async function pickVersionByMenu(candidates, allTags) {
  const dirIdx = await selectMenu('第一步：选择变更方向', candidates.map((c) => `${c.version}  ${c.label}`));
  if (dirIdx === null) { console.log('\n已取消，未创建 tag。'); process.exit(0); }
  const stageIdx = await selectMenu(`第二步：选择版本类型（基于 ${candidates[dirIdx].version}）`,
    ['正式版', 'alpha（内部测试）', 'beta（公测）', 'rc（发布候选）']);
  if (stageIdx === null) { console.log('\n已取消，未创建 tag。'); process.exit(0); }
  const stage = stageIdx === 0 ? '' : PRERELEASE_LABELS[stageIdx - 1];
  const version = stage ? `${candidates[dirIdx].version}-${stage}` : candidates[dirIdx].version;
  return acceptSelectedVersion(version, allTags);
}

/**
 * pickVersionByNumber 编号输入（raw mode 不可用时的降级路径）。
 *
 * 输入规则：1..3 取正式版；1-alpha / 2-beta / 3-rc 取预发布；也可输入完整版本号。
 * 直接回车不取默认；最多重试 3 次。
 *
 * @param {Array<{version: string}>} candidates 三个方向的候选
 * @param {string[]} allTags 本地全部 v* tag
 * @returns {Promise<string>} 选中的版本号
 * @throws 连续输入无法识别时以退出码 1 结束
 */
async function pickVersionByNumber(candidates, allTags) {
  const usage = '  输入 1-3 取正式版；1-alpha / 2-beta / 3-rc 取预发布；也可输入完整版本号；直接回车不会自动发布';
  const rl = createInterface({ input: process.stdin, output: process.stdout });
  const ask = (q) => new Promise((resolve) => rl.question(q, resolve));
  try {
    for (let attempt = 0; attempt < 3; attempt++) {
      const answer = String(await ask('  输入序号 / 序号-阶段 / 完整版本号（必填）: ') ?? '').trim();
      if (!answer) {
        console.warn('  请显式选择版本：直接回车不会自动发布');
        console.warn(usage);
        continue;
      }
      const result = interpretVersionAnswer(answer, candidates);
      if (result.error) {
        console.warn(`  无法识别 "${answer}"：${result.error}`);
        console.warn(usage);
        continue;
      }
      return acceptSelectedVersion(result.version, allTags);
    }
    die('未选择有效版本；请显式传版本号，例如：node scripts/release.mjs 1-rc');
  } finally {
    rl.close();
  }
}

/**
 * pickVersion 版本选择入口：显式参数优先，否则交互选择。
 *
 * 显式参数既接受完整版本号（v0.4.3 / v0.4.3-rc），也接受方向写法（1 / 2-rc）。
 * 省略时打印 12 项候选清单：TTY raw mode 走两步方向键菜单（选方向 → 选阶段），
 * raw mode 不可用降级为编号输入；非 TTY 直接报错并列出候选（不隐式取默认）。
 *
 * @param {Array<{version: string, kind: string, label: string}>} candidates 三个方向的候选
 * @param {string} explicit 用户显式传入的版本号/方向写法，空串表示交互选择
 * @param {string[]} allTags 本地全部 v* tag（用于同 core 冲突预检）
 * @returns {Promise<string>} 选定的版本号
 * @throws 版本非法/交互取消输入超限时 die；Esc 取消以 0 退出
 */
async function pickVersion(candidates, explicit, allTags) {
  if (explicit) return resolveExplicitVersion(explicit, candidates);
  printCandidateCatalog(buildCandidateCatalog(allTags, candidates));
  console.log('  同一版本号只会出现一次；正式版必须使用下一个版本号');
  if (!process.stdin.isTTY) {
    die(`未指定发布版本（非 TTY 不会自动取默认），请显式传入，例如：
  node scripts/release.mjs ${candidates[0].version}
  node scripts/release.mjs 1-alpha`);
  }
  if (canUseRawMode()) return pickVersionByMenu(candidates, allTags);
  console.warn('  提示: 当前终端不支持方向键选择（raw mode 不可用），已降级为编号输入');
  return pickVersionByNumber(candidates, allTags);
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
  const candidates = bumpCandidates(prev);
  const tag = await pickVersion(candidates, opts.version, listAllTags());
  const created = createTag(tag, opts.resume);
  if (opts.push) {
    pushTag(tag);
  } else if (created) {
    console.log(`\n下一步：确认无误后推送发布 → node scripts/release.mjs ${tag.replace(/^v/, '')} --push --resume`);
  }
}

main().catch((err) => die(err?.stack ?? String(err)));
