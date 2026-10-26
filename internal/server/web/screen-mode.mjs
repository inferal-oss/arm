// macOS Screen Sharing accepts the normal RFB framebuffer messages while
// using its own SetMode message to distinguish observation from control.
// Keep the mode explicit even when noVNC negotiated the generic RFB version.
export function sendMacScreenMode(rfb, viewOnly) {
  if (rfb?._rfbConnectionState !== 'connected') return false;
  rfb._sock.sQpushBytes(Uint8Array.of(0x0a, 0, 0, viewOnly ? 0 : 1));
  rfb._sock.flush();
  return true;
}
