<template>
  <canvas
    ref="canvasEl"
    class="block h-full w-full"
    aria-hidden="true"
    @pointermove="handlePointerMove"
    @pointerleave="handlePointerLeave"
  ></canvas>
</template>

<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue'
import { generateBuilding, GRID_SIZE, type Building, type Voxel } from './voxelBuildings'

/**
 * VoxelField - 等距立体方块世界（我的世界 / 乐高风）
 *
 * 一块 12x12 的草地，小房子/高楼按时间循环：
 * 搭脚手架 -> 逐块搭建（方块从天而降）-> 拆脚手架 -> 停留 -> 逐块拆除，
 * 然后换一栋重新建设。鼠标划过时，附近的方块顶块会弹跳起伏。
 */

// ---------------------------------------------------------------------------
// 时间轴参数（毫秒）
// ---------------------------------------------------------------------------
const SCAFFOLD_INTERVAL = 110
const BUILD_INTERVAL = 85
const UNSCAFFOLD_INTERVAL = 70
const HOLD_MS = 3200
const DEMOLISH_INTERVAL = 60
const POP_MS = 240 // 方块落下弹入
const FADE_MS = 160 // 方块消失淡出

// 画布布局预留的最大建筑高度
const MAX_K_WORLD = 9

type Phase = 'scaffold' | 'build' | 'unscaffold' | 'hold' | 'demolish'

interface Placed {
  v: Voxel
  born: number
  died: number // 0 = 存活；>0 = 正在淡出
}

const canvasEl = ref<HTMLCanvasElement | null>(null)

let ctx: CanvasRenderingContext2D | null = null
let rafId = 0
let resizeObserver: ResizeObserver | null = null
let themeObserver: MutationObserver | null = null
let motionMql: MediaQueryList | null = null
let reducedMotion = false
let isDark = false

let cssWidth = 0
let cssHeight = 0
let unit = 16 // 等距单位：地面菱形横对角线的一半
let blockH = 10 // 方块竖直边长
let originX = 0
let originY = 0

let cycle = 0
let building: Building | null = null
let phase: Phase = 'scaffold'
let phaseStart = 0
let lastPlace = 0
let nextIndex = 0
let scaffoldOrder: Voxel[] = []
let buildOrder: Voxel[] = []
let demolishOrder: Voxel[] = []
let demolishDoneAt = 0
const placed = new Map<string, Placed>()
let hoverCell: { i: number; j: number } | null = null

function keyOf(v: Voxel): string {
  return `${v.i},${v.j},${v.k}`
}

/** 层内随机、层间有序的播放顺序 */
function makeOrder(voxels: Voxel[], asc: boolean): Voxel[] {
  const byLayer = new Map<number, Voxel[]>()
  for (const v of voxels) {
    const arr = byLayer.get(v.k) ?? []
    arr.push(v)
    byLayer.set(v.k, arr)
  }
  const layers = [...byLayer.keys()].sort((a, b) => (asc ? a - b : b - a))
  const out: Voxel[] = []
  for (const k of layers) {
    const arr = byLayer.get(k)!
    for (let i = arr.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1))
      ;[arr[i], arr[j]] = [arr[j], arr[i]]
    }
    out.push(...arr)
  }
  return out
}

function startCycle() {
  building = generateBuilding(Math.floor(Math.random() * 0x7fffffff) + cycle * 7919)
  scaffoldOrder = makeOrder(building.scaffold, true)
  buildOrder = makeOrder(building.voxels, true)
  demolishOrder = makeOrder(building.voxels, false)
  demolishDoneAt = 0
  placed.clear()
  phase = 'scaffold'
  nextIndex = 0
}

function place(v: Voxel, now: number) {
  placed.set(keyOf(v), { v, born: now, died: 0 })
}

function markDied(v: Voxel, now: number) {
  const p = placed.get(keyOf(v))
  if (p && p.died === 0) p.died = now
}

function tickPhase(now: number) {
  if (!building) return
  if (phase === 'scaffold') {
    while (nextIndex < scaffoldOrder.length && now - lastPlace >= SCAFFOLD_INTERVAL) {
      lastPlace += SCAFFOLD_INTERVAL
      place(scaffoldOrder[nextIndex++], lastPlace)
    }
    if (nextIndex >= scaffoldOrder.length) {
      phase = 'build'
      nextIndex = 0
      lastPlace = now
    }
  } else if (phase === 'build') {
    while (nextIndex < buildOrder.length && now - lastPlace >= BUILD_INTERVAL) {
      lastPlace += BUILD_INTERVAL
      place(buildOrder[nextIndex++], lastPlace)
    }
    if (nextIndex >= buildOrder.length) {
      phase = 'unscaffold'
      nextIndex = 0
      lastPlace = now
      scaffoldOrder = makeOrder(building.scaffold, false)
    }
  } else if (phase === 'unscaffold') {
    while (nextIndex < scaffoldOrder.length && now - lastPlace >= UNSCAFFOLD_INTERVAL) {
      lastPlace += UNSCAFFOLD_INTERVAL
      markDied(scaffoldOrder[nextIndex++], lastPlace)
    }
    if (nextIndex >= scaffoldOrder.length) {
      phase = 'hold'
      phaseStart = now
    }
  } else if (phase === 'hold') {
    if (now - phaseStart >= HOLD_MS) {
      phase = 'demolish'
      nextIndex = 0
      lastPlace = now
    }
  } else if (phase === 'demolish') {
    while (nextIndex < demolishOrder.length && now - lastPlace >= DEMOLISH_INTERVAL) {
      lastPlace += DEMOLISH_INTERVAL
      markDied(demolishOrder[nextIndex++], lastPlace)
    }
    if (nextIndex >= demolishOrder.length) {
      if (demolishDoneAt === 0) demolishDoneAt = now
      // 等最后一批方块淡出结束，再开始新周期
      if (now - demolishDoneAt >= FADE_MS) {
        demolishDoneAt = 0
        cycle++
        startCycle()
        lastPlace = now
      }
    }
  }

  // 清理已完全淡出的方块
  for (const [key, p] of placed) {
    if (p.died > 0 && now - p.died > FADE_MS) placed.delete(key)
  }
}

// ---------------------------------------------------------------------------
// 渲染
// ---------------------------------------------------------------------------
function shade(v: Voxel, f: number, alpha: number): string {
  const tf = isDark ? 0.62 : 1
  const r = Math.min(255, Math.round(v.r * f * tf))
  const g = Math.min(255, Math.round(v.g * f * tf))
  const b = Math.min(255, Math.round(v.b * f * tf))
  return `rgba(${r}, ${g}, ${b}, ${alpha.toFixed(3)})`
}

function easeOutCubic(t: number): number {
  return 1 - Math.pow(1 - t, 3)
}

function drawGround() {
  if (!ctx) return
  const g1: [number, number, number] = [126, 184, 74]
  const g2: [number, number, number] = [112, 166, 62]
  for (let s = 0; s <= 2 * (GRID_SIZE - 1); s++) {
    for (let i = 0; i < GRID_SIZE; i++) {
      const j = s - i
      if (j < 0 || j >= GRID_SIZE) continue
      const sx = originX + (i - j) * unit
      const sy = originY + (i + j) * (unit / 2)
      const c = (i + j) % 2 === 0 ? g1 : g2
      const tf = isDark ? 0.5 : 1
      ctx.fillStyle = `rgb(${Math.round(c[0] * tf)}, ${Math.round(c[1] * tf)}, ${Math.round(c[2] * tf)})`
      ctx.beginPath()
      ctx.moveTo(sx, sy - unit / 2)
      ctx.lineTo(sx + unit, sy)
      ctx.lineTo(sx, sy + unit / 2)
      ctx.lineTo(sx - unit, sy)
      ctx.closePath()
      ctx.fill()
    }
  }
}

function drawCube(x: number, y: number, v: Voxel, alpha: number, isTop: boolean) {
  if (!ctx) return
  const u = unit
  const bh = blockH
  // 顶面
  ctx.fillStyle = shade(v, 1, alpha)
  ctx.beginPath()
  ctx.moveTo(x, y - u / 2)
  ctx.lineTo(x + u, y)
  ctx.lineTo(x, y + u / 2)
  ctx.lineTo(x - u, y)
  ctx.closePath()
  ctx.fill()
  // 左面
  ctx.fillStyle = shade(v, 0.8, alpha)
  ctx.beginPath()
  ctx.moveTo(x - u, y)
  ctx.lineTo(x, y + u / 2)
  ctx.lineTo(x, y + u / 2 + bh)
  ctx.lineTo(x - u, y + bh)
  ctx.closePath()
  ctx.fill()
  // 右面
  ctx.fillStyle = shade(v, 0.6, alpha)
  ctx.beginPath()
  ctx.moveTo(x + u, y)
  ctx.lineTo(x, y + u / 2)
  ctx.lineTo(x, y + u / 2 + bh)
  ctx.lineTo(x + u, y + bh)
  ctx.closePath()
  ctx.fill()
  // 乐高凸点（仅每层最顶上的方块）
  if (isTop && alpha >= 1) {
    ctx.fillStyle = shade(v, 1.18, 1)
    ctx.beginPath()
    ctx.moveTo(x, y - u * 0.18)
    ctx.lineTo(x + u * 0.36, y)
    ctx.lineTo(x, y + u * 0.18)
    ctx.lineTo(x - u * 0.36, y)
    ctx.closePath()
    ctx.fill()
  }
}

function hoverLift(i: number, j: number, now: number): number {
  if (!hoverCell) return 0
  const d = Math.max(Math.abs(i - hoverCell.i), Math.abs(j - hoverCell.j))
  if (d > 2) return 0
  return blockH * 0.55 * (1 - d / 3) * (0.6 + 0.4 * Math.sin(now / 120 - d * 1.2))
}

function draw(now: number) {
  if (!ctx) return
  ctx.clearRect(0, 0, cssWidth, cssHeight)
  drawGround()

  // 每格最高方块的 key（乐高凸点 + 鼠标弹跳只作用于顶块）
  const topOfCell = new Map<string, { key: string; k: number }>()
  for (const [key, p] of placed) {
    const cellKey = `${p.v.i},${p.v.j}`
    const cur = topOfCell.get(cellKey)
    if (!cur || p.v.k > cur.k) topOfCell.set(cellKey, { key, k: p.v.k })
  }

  const list = [...placed.entries()].sort((a, b) => {
    const va = a[1].v
    const vb = b[1].v
    return va.i + va.j - (vb.i + vb.j) || va.i - vb.i || va.k - vb.k
  })

  for (const [key, p] of list) {
    const { v } = p
    let alpha = 1
    let off = 0
    if (p.died > 0) {
      const q = Math.min(1, (now - p.died) / FADE_MS)
      alpha = 1 - q
    } else {
      const age = now - p.born
      if (age < POP_MS) {
        off = (1 - easeOutCubic(age / POP_MS)) * 2.5 * blockH
      }
      // 鼠标附近的顶块弹跳
      if (topOfCell.get(`${v.i},${v.j}`)?.key === key) {
        off += hoverLift(v.i, v.j, now)
      }
    }
    const sx = originX + (v.i - v.j) * unit
    const sy = originY + (v.i + v.j) * (unit / 2) - (v.k + 1) * blockH - off
    drawCube(sx, sy, v, alpha, topOfCell.get(`${v.i},${v.j}`)?.key === key)
  }
}

function tick(now: number) {
  tickPhase(now)
  draw(now)
  rafId = requestAnimationFrame(tick)
}

// ---------------------------------------------------------------------------
// 布局 / 事件
// ---------------------------------------------------------------------------
function rebuildLayout() {
  const canvas = canvasEl.value
  if (!canvas) return
  cssWidth = canvas.clientWidth
  cssHeight = canvas.clientHeight
  if (cssWidth === 0 || cssHeight === 0) return

  const dpr = Math.min(window.devicePixelRatio || 1, 2)
  canvas.width = Math.round(cssWidth * dpr)
  canvas.height = Math.round(cssHeight * dpr)
  ctx?.setTransform(dpr, 0, 0, dpr, 0, 0)

  unit = Math.max(
    7,
    Math.floor(
      Math.min(cssWidth / (2 * GRID_SIZE + 1), cssHeight / (GRID_SIZE + 0.6 * (MAX_K_WORLD + 1) + 1))
    )
  )
  blockH = Math.max(4, Math.round(unit * 0.6))
  originX = cssWidth / 2
  originY = 8 + MAX_K_WORLD * blockH + unit / 2
}

function handlePointerMove(e: PointerEvent) {
  if (unit === 0) return
  const canvas = canvasEl.value
  if (!canvas) return
  const rect = canvas.getBoundingClientRect()
  const mx = e.clientX - rect.left
  const my = e.clientY - rect.top
  const diff = (mx - originX) / unit
  // 地面反投影作为初始猜测
  let sum = ((my - originY) * 2) / unit
  let i = Math.round((sum + diff) / 2)
  let j = Math.round((sum - diff) / 2)
  // 迭代补偿高度：可见顶面比地面高 (k+1)*blockH，需加回后再反算
  for (let iter = 0; iter < 3; iter++) {
    const lift = (topKAt(i, j) + 1) * blockH
    sum = ((my - originY + lift) * 2) / unit
    const ni = Math.round((sum + diff) / 2)
    const nj = Math.round((sum - diff) / 2)
    if (ni === i && nj === j) break
    i = ni
    j = nj
  }
  if (i >= 0 && i < GRID_SIZE && j >= 0 && j < GRID_SIZE) {
    hoverCell = { i, j }
  } else {
    hoverCell = null
  }
}

/** 某格当前已放置的最高层（无方块返回 -1） */
function topKAt(i: number, j: number): number {
  let top = -1
  for (const p of placed.values()) {
    if (p.v.i === i && p.v.j === j && p.v.k > top) top = p.v.k
  }
  return top
}

function handlePointerLeave() {
  hoverCell = null
}

function syncTheme() {
  isDark = document.documentElement.classList.contains('dark')
}

function drawStatic() {
  draw(performance.now() + POP_MS + FADE_MS)
}

function handleMotionChange() {
  reducedMotion = motionMql?.matches ?? false
  cancelAnimationFrame(rafId)
  if (reducedMotion) {
    // 静态模式：直接呈现完整建筑
    placed.clear()
    if (building) {
      const t = performance.now()
      for (const v of building.voxels) placed.set(keyOf(v), { v, born: t, died: 0 })
    }
    drawStatic()
  } else {
    startCycle()
    lastPlace = performance.now()
    rafId = requestAnimationFrame(tick)
  }
}

onMounted(() => {
  const canvas = canvasEl.value
  if (!canvas) return
  ctx = canvas.getContext('2d')
  if (!ctx) return

  motionMql = window.matchMedia('(prefers-reduced-motion: reduce)')
  reducedMotion = motionMql.matches
  motionMql.addEventListener('change', handleMotionChange)
  syncTheme()

  resizeObserver = new ResizeObserver(() => {
    rebuildLayout()
    if (reducedMotion) drawStatic()
  })
  resizeObserver.observe(canvas)

  themeObserver = new MutationObserver(() => {
    syncTheme()
    if (reducedMotion) drawStatic()
  })
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })

  rebuildLayout()
  startCycle()
  lastPlace = performance.now()

  if (reducedMotion) {
    handleMotionChange()
  } else {
    rafId = requestAnimationFrame(tick)
  }
})

onBeforeUnmount(() => {
  cancelAnimationFrame(rafId)
  resizeObserver?.disconnect()
  themeObserver?.disconnect()
  motionMql?.removeEventListener('change', handleMotionChange)
})
</script>
