// Stock Semblance keyboard hints use the same Apple platform choice as its
// native event handler. Set the host marker before the runtime renders hints.
export function markKeyboardPlatform(document,platform='') {
 const root=document.documentElement;
 if(/Mac|iPhone|iPad/.test(platform))root.setAttribute('data-sem-platform','apple');
 else root.removeAttribute('data-sem-platform');
}
