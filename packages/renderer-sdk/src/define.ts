import type { MountProps, RendererBundle, RendererInstance } from "@fhr/types";

/** Optional teardown returned by a render pass. */
export type RenderCleanup = void | (() => void);

/**
 * Draw the renderer's UI into `container` for the given props. May return a
 * cleanup function that runs before the next render and on unmount.
 *
 * Pure-DOM authors build nodes directly. React authors:
 *   const root = createRoot(container);
 *   root.render(<MyRenderer {...props} />);
 *   return () => root.unmount();
 */
export type RenderFn = (container: HTMLElement, props: MountProps) => RenderCleanup;

export type DefineRendererOptions = {
  handlerId: string;
  extensions: string[];
  /** Content-hash build stamped at bundle-build time (matches binary + wasm). */
  build?: string;
  render: RenderFn;
};

/**
 * Turn a single render function into a RendererBundle with mount/update/unmount
 * lifecycle handled for you. This is the framework-agnostic bundle boundary —
 * consumers call the returned bundle's mount() without knowing what's inside.
 */
export function defineRenderer(opts: DefineRendererOptions): RendererBundle {
  return {
    fhrVersion: 1,
    handlerId: opts.handlerId,
    extensions: opts.extensions,
    build: opts.build,
    mount(el: HTMLElement, props: MountProps): RendererInstance {
      let cleanup: RenderCleanup;
      const draw = (p: MountProps) => {
        if (typeof cleanup === "function") cleanup();
        el.replaceChildren();
        cleanup = opts.render(el, p);
      };
      draw(props);
      return {
        update: (p: MountProps) => draw(p),
        unmount: () => {
          if (typeof cleanup === "function") cleanup();
          el.replaceChildren();
        },
      };
    },
  };
}
