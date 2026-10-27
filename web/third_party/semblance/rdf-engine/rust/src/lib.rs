//! An RDF store and SPARQL engine for Semblance, built as a plain WebAssembly
//! module. The exported surface is `../ABI.md`; see `abi.rs`.
#![no_std]

extern crate alloc;

mod abi;
mod budget;
mod dict;
mod hash;
mod heap;
mod host;
mod idmap;
mod iri;
mod lex;
mod ntriples;
mod sparql;
mod store;
mod turtle;
mod wasm;

#[global_allocator]
static HEAP: heap::Heap = heap::Heap::new();

#[panic_handler]
fn panic(_: &core::panic::PanicInfo) -> ! {
    core::arch::wasm32::unreachable()
}
