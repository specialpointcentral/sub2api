/**
 * VoxelField 体素建筑生成器
 *
 * 程序化生成小房子 / 高楼的体素（voxel）数据与外围脚手架，
 * 供画布按“搭脚手架 -> 逐块搭建 -> 拆脚手架 -> 停留 -> 逐块拆除”循环播放。
 */

export interface Voxel {
  /** 地面网格坐标 */
  i: number
  j: number
  /** 高度层（0 为贴着地面的第一层） */
  k: number
  /** RGB 颜色 */
  r: number
  g: number
  b: number
}

export interface Building {
  /** 建筑本体方块（不含地面与脚手架） */
  voxels: Voxel[]
  /** 脚手架方块 */
  scaffold: Voxel[]
  /** 最高点所在的层（用于画布布局预留顶部空间） */
  maxK: number
}

export const GRID_SIZE = 12

/** 可复现的伪随机数生成器（mulberry32），便于测试 */
export function createRng(seed: number): () => number {
  let a = seed >>> 0
  return () => {
    a |= 0
    a = (a + 0x6d2b79f5) | 0
    let t = Math.imul(a ^ (a >>> 15), 1 | a)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

type Rgb = [number, number, number]

const WALL_COLORS: Rgb[] = [
  [245, 242, 235], // 米白
  [232, 213, 183], // 原木
  [244, 208, 138], // 暖黄
  [187, 222, 235], // 浅蓝
  [240, 196, 196] // 淡粉
]

const ROOF_COLORS: Rgb[] = [
  [214, 69, 62], // 红
  [52, 124, 207], // 蓝
  [237, 145, 53], // 橙
  [96, 125, 139], // 青灰
  [149, 96, 176] // 紫
]

const WOOD: Rgb = [196, 154, 108]
const WOOD_DARK: Rgb = [168, 126, 82]
const DOOR: Rgb = [117, 82, 68]
const GLASS: Rgb = [159, 210, 230]
const STONE: Rgb = [130, 130, 136]

function pick<T>(rng: () => number, arr: T[]): T {
  return arr[Math.floor(rng() * arr.length)]
}

function v(i: number, j: number, k: number, c: Rgb): Voxel {
  return { i, j, k, r: c[0], g: c[1], b: c[2] }
}

/** 生成脚手架：建筑 footprint 四角（外扩一格）的木柱，顶层加横梁色块 */
function genScaffold(x0: number, y0: number, w: number, d: number, topK: number): Voxel[] {
  const poles: Array<[number, number]> = [
    [x0 - 1, y0 - 1],
    [x0 + w, y0 - 1],
    [x0 - 1, y0 + d],
    [x0 + w, y0 + d]
  ]
  // 大房子在前后边中点各加一根柱子
  if (w >= 5) {
    const mid = x0 + Math.floor(w / 2)
    poles.push([mid, y0 - 1], [mid, y0 + d])
  }
  const out: Voxel[] = []
  for (const [i, j] of poles) {
    for (let k = 0; k <= topK; k++) {
      // 隔层换深色，形成脚手架横档的感觉
      out.push(v(i, j, k, k % 2 === 0 ? WOOD : WOOD_DARK))
    }
  }
  return out
}

interface Footprint {
  x0: number
  y0: number
  w: number
  d: number
}

/** 把 footprint（含脚手架外扩一格）约束在网格内，并返回居中偏随机位置 */
function placeFootprint(rng: () => number, grid: number, w: number, d: number): Footprint {
  const maxX = grid - 2 - w
  const maxY = grid - 2 - d
  const cx = Math.floor((grid - w) / 2)
  const cy = Math.floor((grid - d) / 2)
  const x0 = Math.max(1, Math.min(maxX, cx + Math.floor(rng() * 3) - 1))
  const y0 = Math.max(1, Math.min(maxY, cy + Math.floor(rng() * 3) - 1))
  return { x0, y0, w, d }
}

function isPerimeter(x: number, y: number, fp: Footprint): boolean {
  return x === fp.x0 || x === fp.x0 + fp.w - 1 || y === fp.y0 || y === fp.y0 + fp.d - 1
}

/** 坡屋顶：每升高一层四边各内收一格 */
function addPyramidRoof(out: Voxel[], fp: Footprint, baseK: number, color: Rgb): number {
  let r = 0
  let k = baseK
  for (;;) {
    const xa = fp.x0 + r
    const xb = fp.x0 + fp.w - 1 - r
    const ya = fp.y0 + r
    const yb = fp.y0 + fp.d - 1 - r
    if (xa > xb || ya > yb) break
    for (let x = xa; x <= xb; x++) {
      for (let y = ya; y <= yb; y++) {
        out.push(v(x, y, k, color))
      }
    }
    r++
    k++
  }
  return k - 1 // 屋顶最高层
}

/** 小房子：矩形墙体 + 门窗 + 坡屋顶（可选烟囱） */
function genCottage(rng: () => number, grid: number, wallH: number): Building {
  // 随 grid 收窄尺寸：footprint 加一格脚手架边距后仍需放进网格
  const w = Math.min(3 + Math.floor(rng() * 3), grid - 3)
  const d = Math.min(3 + Math.floor(rng() * 3), grid - 3)
  const fp = placeFootprint(rng, grid, w, d)
  const wall = pick(rng, WALL_COLORS)
  const roof = pick(rng, ROOF_COLORS)

  const voxels: Voxel[] = []
  const doorX = fp.x0 + Math.floor(fp.w / 2)
  const frontY = fp.y0 + fp.d - 1

  for (let x = fp.x0; x < fp.x0 + fp.w; x++) {
    for (let y = fp.y0; y < fp.y0 + fp.d; y++) {
      if (!isPerimeter(x, y, fp)) continue
      for (let k = 0; k < wallH; k++) {
        // 门：前面正中，底层（两层高则占两层）
        if (y === frontY && x === doorX && k <= (wallH > 2 ? 1 : 0)) {
          voxels.push(v(x, y, k, DOOR))
          continue
        }
        // 窗：第二层上的墙体随机换成玻璃
        if (k === wallH - 1 && wallH > 1 && !(y === frontY && x === doorX) && rng() < 0.35) {
          voxels.push(v(x, y, k, GLASS))
          continue
        }
        voxels.push(v(x, y, k, wall))
      }
    }
  }

  const roofTop = addPyramidRoof(voxels, fp, wallH, roof)

  // 烟囱（替换同坐标的屋顶方块，避免重复坐标）
  if (rng() < 0.5) {
    const cx = fp.x0 + fp.w - 2
    const cy = fp.y0 + 1
    for (let k = wallH; k <= roofTop + 1; k++) {
      const dup = voxels.findIndex((vv) => vv.i === cx && vv.j === cy && vv.k === k)
      if (dup >= 0) voxels.splice(dup, 1)
      voxels.push(v(cx, cy, k, STONE))
    }
  }

  const maxK = roofTop + 2
  return { voxels, scaffold: genScaffold(fp.x0, fp.y0, fp.w, fp.d, maxK), maxK }
}

/** 高楼：3x3 塔楼，隔层一圈玻璃幕墙，顶部小坡屋顶 */
function genTower(rng: () => number, grid: number): Building {
  const fp = placeFootprint(rng, grid, 3, 3)
  const wallH = 5 + Math.floor(rng() * 2)
  const wall = pick(rng, WALL_COLORS)
  const roof = pick(rng, ROOF_COLORS)

  const voxels: Voxel[] = []
  for (let x = fp.x0; x < fp.x0 + 3; x++) {
    for (let y = fp.y0; y < fp.y0 + 3; y++) {
      if (!isPerimeter(x, y, fp)) continue
      for (let k = 0; k < wallH; k++) {
        // 底层入口 + 每两层一圈玻璃
        if (k === 0 && y === fp.y0 + 2 && x === fp.x0 + 1) {
          voxels.push(v(x, y, k, DOOR))
        } else if (k > 0 && k % 2 === 0) {
          voxels.push(v(x, y, k, GLASS))
        } else {
          voxels.push(v(x, y, k, wall))
        }
      }
    }
  }

  const roofTop = addPyramidRoof(voxels, fp, wallH, roof)
  const maxK = roofTop + 1
  return { voxels, scaffold: genScaffold(fp.x0, fp.y0, fp.w, fp.d, maxK), maxK }
}

/**
 * 生成一个随机建筑（小房子或高楼）。
 * 返回的 voxels 不含顺序信息，播放顺序由渲染组件决定。
 * grid 至少为 6；建筑尺寸会随 grid 收窄，保证含脚手架边距也不越界。
 */
export function generateBuilding(seed: number, grid: number = GRID_SIZE): Building {
  if (grid < 6) {
    throw new RangeError(`grid must be >= 6 to fit a building with scaffolding, got ${grid}`)
  }
  const rng = createRng(seed)
  if (rng() < 0.7) {
    return genCottage(rng, grid, 2 + Math.floor(rng() * 2))
  }
  return genTower(rng, grid)
}
