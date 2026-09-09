// The viewport's one callout: what is selected, where it is, and its number.
//
// One, not one per change. The view-management literature is unambiguous that
// labels pinned to every feature occlude the thing they describe and each other,
// and the tools that ship persistent per-change labels in 3D are the ones
// reviewers turn off. So the viewport gets exactly the selected change: a dot on
// the geometry, a short leader line, and the headline number
// (`review.ts:headline`). The panel owns everything else.
//
// The label is DOM, not a sprite: it stays crisp, it costs no texture memory, and
// it can't be occluded by the model. Per frame it does one projection and two
// style writes.

import type { Camera, Vector3 } from "three";

/** Where a world point lands in a viewport, in CSS pixels. */
export type ScreenPoint = { x: number; y: number; visible: boolean };

export type Viewport = { width: number; height: number };

/** Project a world point into viewport pixels. `visible` is false behind the camera. */
export function projectToScreen(point: Vector3, camera: Camera, view: Viewport): ScreenPoint {
  const ndc = point.clone().project(camera);
  return {
    x: (ndc.x * 0.5 + 0.5) * view.width,
    y: (-ndc.y * 0.5 + 0.5) * view.height,
    visible: ndc.z >= -1 && ndc.z <= 1,
  };
}

/** How far the label sits from the point it describes. */
const OFFSET = { x: 34, y: -30 };
/** Enough room for a label; past this, the leader flips to the other side. */
const LABEL_WIDTH = 150;

export type Placement = {
  /** Anchor position for the whole assembly, in viewport pixels. */
  left: number;
  top: number;
  /** True when the label hangs to the left of the anchor instead of the right. */
  flip: boolean;
  /** False when the anchor isn't on screen at all — hide, don't guess. */
  onScreen: boolean;
};

/**
 * Where to put the callout for a projected point: offset up and to the right,
 * flipped to the left when there isn't room, and hidden outright when the anchor
 * is off screen or behind the camera. A callout pinned to the viewport edge for a
 * change that isn't in view is a lie about where the change is.
 */
export function calloutPlacement(point: ScreenPoint, view: Viewport): Placement {
  const margin = 4;
  const onScreen =
    point.visible &&
    point.x >= -margin &&
    point.y >= -margin &&
    point.x <= view.width + margin &&
    point.y <= view.height + margin;
  const flip = point.x + OFFSET.x + LABEL_WIDTH > view.width;
  return { left: point.x, top: point.y, flip, onScreen };
}

export type Callout = {
  /** Show the callout for a change. Position comes from the next `place`. */
  show(title: string, headline: string): void;
  hide(): void;
  /** Move it to a projected point (called from the frame loop). */
  place(point: ScreenPoint, view: Viewport): void;
  readonly visible: boolean;
  dispose(): void;
};

const LEADER_LENGTH = Math.round(Math.hypot(OFFSET.x, OFFSET.y));
const LEADER_ANGLE = (Math.atan2(OFFSET.y, OFFSET.x) * 180) / Math.PI;

/**
 * Build the callout's DOM inside `container` (which must be positioned). Hidden
 * until `show`.
 */
export function createCallout(container: HTMLElement, theme: "light" | "dark" = "light"): Callout {
  const doc = container.ownerDocument;
  const dark = theme === "dark";
  const ink = dark ? "#e6edf3" : "#1f2328";
  const paper = dark ? "rgba(13,17,23,0.86)" : "rgba(255,255,255,0.9)";
  const line = dark ? "#8b949e" : "#57606a";

  const root = doc.createElement("div");
  root.style.cssText =
    "position:absolute;left:0;top:0;pointer-events:none;z-index:2;will-change:transform";
  // Set apart from cssText because show/hide/place rewrite exactly this property.
  root.style.display = "none";

  const dot = doc.createElement("div");
  dot.style.cssText =
    `position:absolute;left:-4px;top:-4px;width:8px;height:8px;border-radius:50%;background:${line};` +
    `box-shadow:0 0 0 2px ${paper}`;

  const leader = doc.createElement("div");
  leader.style.cssText =
    `position:absolute;left:0;top:0;height:1px;width:${LEADER_LENGTH}px;background:${line};` +
    `transform-origin:0 50%;transform:rotate(${LEADER_ANGLE}deg)`;

  const label = doc.createElement("div");
  label.style.cssText =
    `position:absolute;left:${OFFSET.x}px;top:${OFFSET.y}px;transform:translateY(-50%);` +
    `max-width:${LABEL_WIDTH}px;padding:4px 8px;border-radius:6px;background:${paper};color:${ink};` +
    `font:12px/1.35 ui-sans-serif,system-ui,sans-serif;white-space:nowrap;overflow:hidden;` +
    `text-overflow:ellipsis;box-shadow:0 1px 4px rgba(0,0,0,0.25)`;

  const titleEl = doc.createElement("div");
  titleEl.style.cssText = "opacity:0.7;font-size:11px";
  const headlineEl = doc.createElement("div");
  headlineEl.style.cssText = "font-weight:600";
  label.append(titleEl, headlineEl);
  root.append(dot, leader, label);
  container.appendChild(root);

  let visible = false;
  const setFlip = (flip: boolean): void => {
    leader.style.transform = `rotate(${flip ? 180 - LEADER_ANGLE : LEADER_ANGLE}deg)`;
    label.style.left = flip ? `${-OFFSET.x}px` : `${OFFSET.x}px`;
    label.style.transform = flip ? "translate(-100%, -50%)" : "translateY(-50%)";
  };

  return {
    show(title: string, headline: string): void {
      titleEl.textContent = title;
      headlineEl.textContent = headline;
      visible = true;
      root.style.display = "block";
    },
    hide(): void {
      visible = false;
      root.style.display = "none";
    },
    place(point: ScreenPoint, view: Viewport): void {
      if (!visible) return;
      const placement = calloutPlacement(point, view);
      if (!placement.onScreen) {
        root.style.display = "none";
        return;
      }
      root.style.display = "block";
      root.style.transform = `translate(${Math.round(placement.left)}px, ${Math.round(placement.top)}px)`;
      setFlip(placement.flip);
    },
    get visible(): boolean {
      return visible;
    },
    dispose(): void {
      root.remove();
    },
  };
}
