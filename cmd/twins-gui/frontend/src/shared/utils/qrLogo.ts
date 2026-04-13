/**
 * Creates a circular-bordered version of a logo image for use in QR codes.
 * Returns a data URL of a PNG with the logo centered inside a white circle
 * with a subtle drop shadow, thin outer accent ring, and main border ring.
 *
 * The canvas is slightly larger than `size` to accommodate the shadow bleed.
 * Callers should set imageSettings to a few pixels larger than `canvasSize`
 * (returned indirectly via the image dimensions) for a clean white margin.
 */
export function createCircularLogoDataURL(
  logoSrc: string,
  size: number,
  borderWidth: number,
  borderColor: string,
): Promise<string> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => {
      // Extra canvas space for shadow to bleed into without clipping
      const shadowPad = 3;
      const canvasSize = size + shadowPad * 2;
      const canvas = document.createElement('canvas');
      canvas.width = canvasSize;
      canvas.height = canvasSize;
      const ctx = canvas.getContext('2d');
      if (!ctx) {
        reject(new Error('Canvas 2D context unavailable'));
        return;
      }

      const center = canvasSize / 2;
      const radius = size / 2;

      // Subtle shadow for depth — separates the logo island from QR noise
      ctx.shadowColor = 'rgba(0, 0, 0, 0.12)';
      ctx.shadowBlur = shadowPad;
      ctx.shadowOffsetX = 0;
      ctx.shadowOffsetY = 0;

      // White filled circle background (shadow applies here)
      ctx.beginPath();
      ctx.arc(center, center, radius, 0, Math.PI * 2);
      ctx.fillStyle = '#ffffff';
      ctx.fill();

      // Clear shadow for remaining draws
      ctx.shadowColor = 'transparent';
      ctx.shadowBlur = 0;

      // Thin outer accent ring (semi-transparent, at circle edge)
      ctx.beginPath();
      ctx.arc(center, center, radius - 0.5, 0, Math.PI * 2);
      ctx.strokeStyle = borderColor;
      ctx.lineWidth = 1;
      ctx.globalAlpha = 0.3;
      ctx.stroke();
      ctx.globalAlpha = 1;

      // Main border ring (inset from outer accent)
      ctx.beginPath();
      ctx.arc(center, center, radius - borderWidth / 2 - 1, 0, Math.PI * 2);
      ctx.strokeStyle = borderColor;
      ctx.lineWidth = borderWidth;
      ctx.stroke();

      // Logo centered inside the border with generous padding
      const padding = shadowPad + borderWidth + 6;
      const logoSize = canvasSize - padding * 2;
      ctx.drawImage(img, padding, padding, logoSize, logoSize);

      resolve(canvas.toDataURL('image/png'));
    };
    img.onerror = () => reject(new Error(`Failed to load logo: ${logoSrc}`));
    img.src = logoSrc;
  });
}
