import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import VoxelField from '../VoxelField.vue'
import { generateBuilding, GRID_SIZE } from '../voxelBuildings'

describe('voxelBuildings generator', () => {
  it('generates non-empty buildings with in-bounds voxels for many seeds', () => {
    for (let seed = 1; seed <= 50; seed++) {
      const b = generateBuilding(seed)
      expect(b.voxels.length).toBeGreaterThan(0)
      expect(b.scaffold.length).toBeGreaterThan(0)
      expect(b.maxK).toBeGreaterThan(0)
      for (const v of [...b.voxels, ...b.scaffold]) {
        expect(v.i).toBeGreaterThanOrEqual(0)
        expect(v.i).toBeLessThan(GRID_SIZE)
        expect(v.j).toBeGreaterThanOrEqual(0)
        expect(v.j).toBeLessThan(GRID_SIZE)
        expect(v.k).toBeGreaterThanOrEqual(0)
        expect(v.k).toBeLessThanOrEqual(b.maxK)
        for (const c of [v.r, v.g, v.b]) {
          expect(c).toBeGreaterThanOrEqual(0)
          expect(c).toBeLessThanOrEqual(255)
        }
      }
    }
  })

  it('is deterministic for the same seed', () => {
    const a = generateBuilding(42)
    const b = generateBuilding(42)
    expect(a.voxels).toEqual(b.voxels)
    expect(a.scaffold).toEqual(b.scaffold)
  })

  it('never emits duplicate voxel coordinates', () => {
    for (let seed = 1; seed <= 200; seed++) {
      const b = generateBuilding(seed)
      const keys = new Set(b.voxels.map((v) => `${v.i},${v.j},${v.k}`))
      expect(keys.size).toBe(b.voxels.length)
    }
  })

  it('keeps voxels in bounds on the smallest supported grid', () => {
    for (let seed = 1; seed <= 200; seed++) {
      const b = generateBuilding(seed, 6)
      for (const v of [...b.voxels, ...b.scaffold]) {
        expect(v.i).toBeGreaterThanOrEqual(0)
        expect(v.i).toBeLessThan(6)
        expect(v.j).toBeGreaterThanOrEqual(0)
        expect(v.j).toBeLessThan(6)
      }
    }
  })

  it('rejects grids too small to build on', () => {
    expect(() => generateBuilding(1, 5)).toThrow(RangeError)
  })
})

describe('VoxelField component', () => {
  it('mounts gracefully when the canvas 2d context is unavailable (jsdom)', () => {
    const getContext = vi
      .spyOn(HTMLCanvasElement.prototype, 'getContext')
      .mockImplementation(() => null)

    const wrapper = mount(VoxelField)
    expect(wrapper.find('canvas').exists()).toBe(true)

    wrapper.unmount()
    getContext.mockRestore()
  })
})
