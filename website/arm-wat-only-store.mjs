// Keep the pinned framework's optional loader contract without shipping its
// unused Rust engine. The website explicitly initializes the WAT store.
export default async function unsupportedOxigraphStore() {
  throw new Error('The ARM website supports the pinned WAT store only; Oxigraph and Hybrid stores are not included.')
}
