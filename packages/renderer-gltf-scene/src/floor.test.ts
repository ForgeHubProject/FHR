import { describe, it, expect } from "vitest";
import { Box3, Vector3 } from "three";
import { DEFAULT_FLOOR, fitFloor, niceStep } from "./floor.js";

const box = (min: [number, number, number], max: [number, number, number]): Box3 =>
  new Box3(new Vector3(...min), new Vector3(...max));

describe("niceStep", () => {
  it("rounds up to 1, 2 or 5 times a power of ten", () => {
    expect(niceStep(1)).toBe(1);
    expect(niceStep(1.1)).toBe(2);
    expect(niceStep(3)).toBe(5);
    expect(niceStep(7)).toBe(10);
    expect(niceStep(0.03)).toBeCloseTo(0.05);
    expect(niceStep(130)).toBe(200);
  });
});

describe("fitFloor — where the grid stands (FHR#86)", () => {
  it("puts the floor under a model centred on the origin, not through its waist", () => {
    const f = fitFloor(box([-1, -1, -1], [1, 1, 1]));
    expect(f.y).toBe(-1);
  });

  it("leaves a feet-at-zero model's floor at zero", () => {
    expect(fitFloor(box([-1, 0, -1], [1, 2, 1])).y).toBe(0);
  });

  it("centres the grid under the content", () => {
    const f = fitFloor(box([10, 0, -4], [14, 2, 0]));
    expect(f.centerX).toBe(12);
    expect(f.centerZ).toBe(-2);
  });

  it("gives a metre-scale model a readable grid that covers it", () => {
    const f = fitFloor(box([-1, 0, -1], [1, 2, 1]));
    const cell = f.size / f.divisions;
    expect(cell).toBe(0.2);
    expect(f.size).toBeGreaterThanOrEqual(2);
    expect(f.divisions % 2).toBe(0);
  });

  it("gives a millimetre-scale model a grid at its own scale, not 40 units", () => {
    const f = fitFloor(box([-150, 0, -100], [150, 80, 100]));
    expect(f.size / f.divisions).toBe(50);
    expect(f.size).toBeGreaterThanOrEqual(300);
  });

  it("gives a tiny model more than one cell", () => {
    const f = fitFloor(box([0, 0, 0], [0.01, 0.01, 0.01]));
    expect(f.divisions).toBeGreaterThanOrEqual(2);
    expect(f.size).toBeLessThan(1);
  });

  it("caps the cell count", () => {
    const f = fitFloor(box([0, 0, 0], [1e6, 1, 1]));
    expect(f.divisions).toBeLessThanOrEqual(200);
  });

  it("falls back to the original grid for an empty box", () => {
    expect(fitFloor(new Box3())).toEqual(DEFAULT_FLOOR);
  });

  it("keeps the content's bottom for a single point", () => {
    const f = fitFloor(box([1, 3, 1], [1, 3, 1]));
    expect(f.y).toBe(3);
    expect(f.size).toBe(DEFAULT_FLOOR.size);
  });
});
