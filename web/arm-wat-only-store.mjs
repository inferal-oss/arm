// ARM selects Semblance's pinned WAT engine explicitly. Keep the generic
// framework loader's optional import contract, without shipping an unused
// Oxigraph/Rust binary or promising an alternate product store.
export default async function unsupportedOxigraphStore() {
 throw new Error('The ARM dashboard supports the pinned WAT store only; Oxigraph and Hybrid stores are not included.');
}
