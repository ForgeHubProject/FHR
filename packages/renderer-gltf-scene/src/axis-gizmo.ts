// The viewport's orientation indicator: which way X, Y and Z are pointing now.
//
// A reviewer who has orbited a few times has no idea which way the model is
// facing any more, and "the front of the car" is not something a diff can tell
// them. Every 3D tool this audience uses answers that with a little axis cross
// in a corner, so this one does too.
//
// DOM, not a second three.js scene. The usual way to draw this is a scissored
// viewport with its own camera and an AxesHelper, which costs a second render
// pass every frame for a widget 56 pixels wide. An orientation gizmo is a
// *rotation readout* — position and perspective are irrelevant to it — so the
// whole thing is three unit vectors run through the inverse camera rotation and
// written out as CSS transforms: no draw call, no texture memory, and crisp at
// any pixel ratio, like the callout and the legend beside it.
//
// The letters are the label, and the colours only agree with them. Red/green/
// blue for X/Y/Z is the convention in every DCC tool (three's own AxesHelper
// included), and departing from it here would cost more than it bought — but
// red and green are the pair palette.ts exists to keep out of this renderer, so
// no axis is identified by its colour alone: X, Y and Z are written on them.
//
// The projection is pure and tested headlessly; three is used for its Quaternion
// arithmetic only, so nothing here creates a WebGL context.

import { Quaternion, Vector3 } from "three";

export type AxisName = "x" | "y" | "z";

/** Where one axis points, in the gizmo's own little square. */
export type AxisTip = {
  axis: AxisName;
  /** Tip offset from the gizmo's centre, in CSS pixels (y already flipped). */
  x: number;
  y: number;
  /** +1 straight at the viewer, −1 straight away: depth, for the fade. */
  toward: number;
};

/** Half the widget's size: how far a tip sits from the centre at full extension. */
export const GIZMO_RADIUS = 24;

/** Axis colours. Reinforcement only — the letters carry the meaning (see above). */
export const AXIS_CSS: Record<AxisName, string> = {
  x: "#e5534b",
  y: "#57ab5a",
  z: "#539bf5",
};

const AXES: readonly { axis: AxisName; dir: Vector3 }[] = [
  { axis: "x", dir: new Vector3(1, 0, 0) },
  { axis: "y", dir: new Vector3(0, 1, 0) },
  { axis: "z", dir: new Vector3(0, 0, 1) },
];

/**
 * Where the three world axes point on screen, for a camera holding this world
 * orientation.
 *
 * Directions only: the world axis is rotated into camera space and its x/y read
 * straight off, which is an orthographic projection of a unit vector — an axis
 * cross must read the same whether the camera is a metre from the model or a
 * kilometre, and a perspective divide would make it swim as the reviewer dollies.
 *
 * Screen y is flipped here rather than at the call site, because every consumer
 * of this is a CSS transform and CSS y grows downwards.
 */
export function axisTips(orientation: Quaternion, radius: number = GIZMO_RADIUS): AxisTip[] {
  const inverse = orientation.clone().invert();
  return AXES.map(({ axis, dir }) => {
    const v = dir.clone().applyQuaternion(inverse);
    // Camera space looks down −Z, so +z is the half of the world facing the
    // viewer — the axes there are drawn solid and the ones behind are faded.
    return { axis, x: v.x * radius, y: -v.y * radius, toward: v.z };
  });
}

export type AxisGizmo = {
  el: HTMLElement;
  /** Re-point the axes. Cheap to call every frame: a no-op unless the camera turned. */
  update(orientation: Quaternion): void;
  dispose(): void;
};

/** How much an axis pointing away from the viewer is dimmed. */
const BEHIND_OPACITY = "0.4";

/**
 * Build the gizmo in `container` (which must be positioned). Sits in the
 * top-right corner: the bottom edge is the blink hint, bottom-right is the
 * heatmap legend and top-left is the side-by-side pane label, and an
 * orientation cross that covered one of those would be a trade nobody asked for.
 */
export function createAxisGizmo(container: HTMLElement, theme: "light" | "dark" = "light"): AxisGizmo {
  const doc = container.ownerDocument;
  const size = GIZMO_RADIUS * 2;
  const ink = theme === "dark" ? "#0d1117" : "#ffffff";

  const el = doc.createElement("div");
  el.setAttribute("data-gizmo", "1");
  // pointer-events:none throughout: this is a readout, not a control. Dragging
  // it to rotate the camera is what the model itself is for, and a widget that
  // ate clicks meant for geometry behind it would be worse than not being there.
  el.style.cssText =
    `position:absolute;right:8px;top:8px;z-index:2;width:${size}px;height:${size}px;` +
    `pointer-events:none`;

  type AxisEls = { line: HTMLElement; tip: HTMLElement };
  const parts = new Map<AxisName, AxisEls>();
  for (const { axis } of AXES) {
    const colour = AXIS_CSS[axis];
    // The line is drawn at full length and scaled down by the projection, so a
    // frame costs one transform write per element and no layout.
    const line = doc.createElement("div");
    line.setAttribute("data-axis", axis);
    line.style.cssText =
      `position:absolute;left:50%;top:50%;width:${GIZMO_RADIUS}px;height:2px;margin-top:-1px;` +
      `background:${colour};transform-origin:0 50%;border-radius:1px`;
    const tip = doc.createElement("div");
    tip.setAttribute("data-axis-tip", axis);
    tip.style.cssText =
      `position:absolute;left:50%;top:50%;width:13px;height:13px;margin:-7px 0 0 -7px;` +
      `border-radius:50%;background:${colour};color:${ink};text-align:center;` +
      `font:700 9px/13px ui-sans-serif,system-ui,sans-serif`;
    tip.textContent = axis.toUpperCase();
    el.append(line, tip);
    parts.set(axis, { line, tip });
  }
  container.appendChild(el);

  // The camera is still on most frames — a reviewer reads far longer than they
  // orbit — and an unchanged orientation writes nothing at all.
  const last = new Quaternion(0, 0, 0, Number.NaN);

  return {
    el,
    update(orientation: Quaternion): void {
      if (orientation.equals(last)) return;
      last.copy(orientation);
      for (const tip of axisTips(orientation)) {
        const part = parts.get(tip.axis);
        if (!part) continue;
        // Rounded, like the callout's placement: a degree of rotation is 0.4px at
        // this radius and a fraction of a pixel of translation is nothing anyone
        // can see, while the full float writes a new string on every frame of a
        // slow orbit and leaves the two halves of an axis disagreeing about where
        // sub-pixel it ends.
        const length = Math.hypot(tip.x, tip.y);
        const angle = Math.round((Math.atan2(tip.y, tip.x) * 180) / Math.PI);
        const scale = Math.round((length / GIZMO_RADIUS) * 100) / 100;
        const opacity = tip.toward >= 0 ? "1" : BEHIND_OPACITY;
        part.line.style.transform = `rotate(${angle}deg) scaleX(${scale})`;
        part.line.style.opacity = opacity;
        part.tip.style.transform = `translate(${Math.round(tip.x)}px, ${Math.round(tip.y)}px)`;
        part.tip.style.opacity = opacity;
      }
    },
    dispose(): void {
      el.remove();
    },
  };
}
