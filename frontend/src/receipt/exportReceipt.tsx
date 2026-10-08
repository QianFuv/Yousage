/** Turn the on-screen receipt into a PNG that matches what the page shows. */
import receiptStyles from "./receipt.css?raw";

/** Transparent margin around the paper so its torn edges are not clipped. */
const IMAGE_MARGIN = 16;

/** Image pixels per CSS pixel. */
const IMAGE_SCALE = 2;

/**
 * Resolve the receipt colors and font against the active theme, so the
 * standalone image does not depend on the application stylesheet.
 *
 * @param paper - The rendered receipt paper element.
 * @returns CSS that pins every receipt variable to a concrete value.
 */
function resolvedVariables(paper: HTMLElement): string {
  const style = getComputedStyle(paper);
  const sample = (selector: string, property: string) => {
    const element = paper.querySelector(selector);
    return element ? getComputedStyle(element).getPropertyValue(property) : "";
  };
  const values = {
    "--receipt-paper": style.backgroundColor,
    "--receipt-ink": style.color,
    "--receipt-accent": sample(".receipt-brand", "color"),
    "--receipt-faint": sample(".receipt-meta", "color"),
    "--receipt-rule": sample(".receipt-rule", "border-top-color"),
    "--receipt-font": style.fontFamily,
  };
  const declarations = Object.entries(values)
    .filter(([, value]) => value)
    .map(([name, value]) => `${name}:${value}`)
    .join(";");
  return `.receipt-paper{${declarations};font-size:${style.fontSize}}`;
}

/**
 * Render a receipt to a PNG at twice its CSS size.
 *
 * @param paper - The rendered receipt paper element.
 * @returns The encoded PNG image.
 */
export async function receiptImage(paper: HTMLElement): Promise<Blob> {
  const width = paper.offsetWidth + IMAGE_MARGIN * 2;
  const height = paper.offsetHeight + IMAGE_MARGIN * 2;
  const rootSize = getComputedStyle(document.documentElement).fontSize;
  const markup = new XMLSerializer().serializeToString(paper);
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}"><foreignObject width="100%" height="100%"><div xmlns="http://www.w3.org/1999/xhtml" style="padding:${IMAGE_MARGIN}px;font-size:${rootSize}"><style>:root{font-size:${rootSize}}${receiptStyles}${resolvedVariables(paper)}</style>${markup}</div></foreignObject></svg>`;
  const image = new Image();
  image.src = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
  await image.decode();
  const canvas = document.createElement("canvas");
  canvas.width = width * IMAGE_SCALE;
  canvas.height = height * IMAGE_SCALE;
  const context = canvas.getContext("2d");
  if (!context) throw new Error("Canvas is unavailable");
  context.scale(IMAGE_SCALE, IMAGE_SCALE);
  context.drawImage(image, 0, 0);
  return new Promise((resolve, reject) =>
    canvas.toBlob(
      (blob) =>
        blob ? resolve(blob) : reject(new Error("Image encoding failed")),
      "image/png",
    ),
  );
}

/**
 * Copy a receipt to the system clipboard as a PNG image.
 *
 * @param paper - The rendered receipt paper element.
 */
export async function copyReceiptImage(paper: HTMLElement): Promise<void> {
  const image = receiptImage(paper);
  await navigator.clipboard.write([new ClipboardItem({ "image/png": image })]);
}
