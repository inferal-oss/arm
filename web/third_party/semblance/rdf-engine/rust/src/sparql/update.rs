//! Applying SPARQL Update. A request applies completely or not at all: every
//! change is logged and undone if a later operation fails.

use super::eval::{Active, Ctx};
use super::*;
use crate::dict::{BNODE, DEFAULT_GRAPH_ID, Dict, IRI};
use crate::store::{Quad, Store};

struct Log<'a> {
    store: &'a mut Store,
    changes: Vec<(bool, Quad)>,
}

impl Log<'_> {
    fn add(&mut self, q: Quad) {
        if self.store.add(q) {
            self.changes.push((true, q));
        }
    }

    fn remove(&mut self, q: Quad) {
        if self.store.remove(q) {
            self.changes.push((false, q));
        }
    }

    fn undo(&mut self) {
        while let Some((added, q)) = self.changes.pop() {
            if added {
                self.store.remove(q);
            } else {
                self.store.add(q);
            }
        }
    }

    fn quads_of(&self, g: u32) -> Vec<Quad> {
        let mut out = Vec::new();
        self.store.scan([0, 0, 0, g], &mut out);
        out.chunks_exact(4).map(|c| [c[0], c[1], c[2], c[3]]).collect()
    }
}

pub fn execute(u: &Update, dict: &mut Dict, store: &mut Store) -> Result<(), &'static str> {
    let mut log = Log { store, changes: Vec::new() };
    for op in &u.ops {
        if let Err(e) = apply(op, dict, &mut log, &u.base) {
            log.undo();
            return Err(e);
        }
    }
    Ok(())
}

/// A quad from a template under a solution; None if a position is unbound or invalid.
fn quad(t: &QuadTemplate, row: &[u32], bnodes: &[u32], default: u32, dict: &mut Dict) -> Option<Quad> {
    let s = inst(&t[0], row, bnodes, dict)?;
    let p = inst(&t[1], row, bnodes, dict)?;
    let o = inst(&t[2], row, bnodes, dict)?;
    let g = match &t[3] {
        Tt::Term(0) => default,
        g => inst(g, row, bnodes, dict)?,
    };
    let kind = |id: u32| dict.term(id).map_or(0, |t| t.kind);
    if !matches!(kind(s), IRI | BNODE) || kind(p) != IRI || !(g == DEFAULT_GRAPH_ID || kind(g) == IRI) {
        return None;
    }
    Some([s, p, o, g])
}

fn apply(op: &UpdateOp, dict: &mut Dict, log: &mut Log, base: &[u8]) -> Result<(), &'static str> {
    match op {
        UpdateOp::Data { insert, quads, bnodes } => {
            let fresh: Vec<u32> = (0..*bnodes).map(|_| dict.fresh_bnode()).collect();
            for t in quads {
                if let Some(q) = quad(t, &[], &fresh, DEFAULT_GRAPH_ID, dict) {
                    if *insert { log.add(q) } else { log.remove(q) }
                }
            }
        }
        UpdateOp::Modify { delete, insert, bnodes, with, using, select, nvars } => {
            let (default, named) = match (using, with) {
                (Some((d, n)), _) => (d.clone(), Some(n.clone())),
                (None, Some(w)) => (alloc::vec![*w], None),
                (None, None) => (alloc::vec![DEFAULT_GRAPH_ID], None),
            };
            let rows = {
                let mut ctx = Ctx::new(dict, log.store, default, named, base, *nvars);
                ctx.select(select, &alloc::vec![0u32; *nvars], Active::Default)
            };
            let target = with.unwrap_or(DEFAULT_GRAPH_ID);
            let mut removes = Vec::new();
            let mut adds = Vec::new();
            for r in &rows {
                for t in delete {
                    if let Some(q) = quad(t, r, &[], target, dict) {
                        removes.push(q);
                    }
                }
                let fresh: Vec<u32> = (0..*bnodes).map(|_| dict.fresh_bnode()).collect();
                for t in insert {
                    if let Some(q) = quad(t, r, &fresh, target, dict) {
                        adds.push(q);
                    }
                }
            }
            removes.into_iter().for_each(|q| log.remove(q));
            adds.into_iter().for_each(|q| log.add(q));
        }
        UpdateOp::Load { silent } => {
            if !silent {
                return Err("LOAD is not supported");
            }
        }
        UpdateOp::Clear { target } => {
            let graphs: Vec<u32> = match target {
                Target::Graph(g) => alloc::vec![*g],
                Target::Default => alloc::vec![DEFAULT_GRAPH_ID],
                Target::Named => log.store.graphs().into_iter().filter(|&g| g != DEFAULT_GRAPH_ID).collect(),
                Target::All => log.store.graphs(),
            };
            for g in graphs {
                for q in log.quads_of(g) {
                    log.remove(q);
                }
            }
        }
        UpdateOp::Create { silent, graph } => {
            // Graphs exist by having quads; creating one that has some is an error.
            if !silent && !log.quads_of(*graph).is_empty() {
                return Err("the graph already exists");
            }
        }
        UpdateOp::Transfer { op, from, to } => {
            let (from, to) = (if *from == 0 { DEFAULT_GRAPH_ID } else { *from }, if *to == 0 { DEFAULT_GRAPH_ID } else { *to });
            if from == to {
                return Ok(());
            }
            let source = log.quads_of(from);
            if *op != Transfer::Add {
                for q in log.quads_of(to) {
                    log.remove(q);
                }
            }
            for q in &source {
                log.add([q[0], q[1], q[2], to]);
            }
            if *op == Transfer::Move {
                for q in source {
                    log.remove(q);
                }
            }
        }
    }
    Ok(())
}
