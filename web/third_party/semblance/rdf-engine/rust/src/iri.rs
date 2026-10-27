//! Relative IRI resolution (RFC 3986, section 5.2).

use alloc::vec::Vec;

struct Parts<'a> {
    scheme: Option<&'a [u8]>,
    authority: Option<&'a [u8]>,
    path: &'a [u8],
    query: Option<&'a [u8]>,
    fragment: Option<&'a [u8]>,
}

fn split(s: &[u8]) -> Parts<'_> {
    let end = |s: &[u8], stops: &[u8]| s.iter().position(|c| stops.contains(c)).unwrap_or(s.len());
    let mut rest = s;
    let mut scheme = None;
    let i = end(rest, b":/?#");
    if i < rest.len() && rest[i] == b':' && i > 0 && rest[0].is_ascii_alphabetic() {
        scheme = Some(&rest[..i]);
        rest = &rest[i + 1..];
    }
    let mut authority = None;
    if rest.starts_with(b"//") {
        let i = 2 + end(&rest[2..], b"/?#");
        authority = Some(&rest[2..i]);
        rest = &rest[i..];
    }
    let i = end(rest, b"?#");
    let path = &rest[..i];
    rest = &rest[i..];
    let mut query = None;
    if rest.first() == Some(&b'?') {
        let i = end(rest, b"#");
        query = Some(&rest[1..i]);
        rest = &rest[i..];
    }
    let fragment = if rest.is_empty() { None } else { Some(&rest[1..]) };
    Parts { scheme, authority, path, query, fragment }
}

fn remove_dot_segments(path: &[u8], out: &mut Vec<u8>) {
    let start = out.len();
    let mut input = path;
    // A buffer for the one rewrite that is not a suffix of the input ("/." -> "/").
    let slash: &[u8] = b"/";
    while !input.is_empty() {
        if input.starts_with(b"../") {
            input = &input[3..];
        } else if input.starts_with(b"./") {
            input = &input[2..];
        } else if input.starts_with(b"/./") {
            input = &input[2..];
        } else if input == b"/." {
            input = slash;
        } else if input.starts_with(b"/../") || input == b"/.." {
            input = if input.len() == 3 { slash } else { &input[3..] };
            let last = out[start..].iter().rposition(|&c| c == b'/').map_or(start, |i| start + i);
            out.truncate(last);
        } else if input == b"." || input == b".." {
            input = b"";
        } else {
            let first = if input[0] == b'/' { 1 } else { 0 };
            let n = input[first..].iter().position(|&c| c == b'/').map_or(input.len(), |i| first + i);
            out.extend_from_slice(&input[..n]);
            input = &input[n..];
        }
    }
}

/// Resolve `r` against `base` into `out`. With an empty base, `r` is kept as is.
pub fn resolve(base: &[u8], r: &[u8], out: &mut Vec<u8>) {
    out.clear();
    if base.is_empty() {
        out.extend_from_slice(r);
        return;
    }
    let r = split(r);
    let b = split(base);
    let (scheme, authority, query);
    if let Some(s) = r.scheme {
        scheme = Some(s);
        authority = r.authority;
        query = r.query;
    } else {
        scheme = b.scheme;
        if r.authority.is_some() {
            authority = r.authority;
            query = r.query;
        } else {
            authority = b.authority;
            query = if r.path.is_empty() { r.query.or(b.query) } else { r.query };
        }
    }
    if let Some(s) = scheme {
        out.extend_from_slice(s);
        out.push(b':');
    }
    if let Some(a) = authority {
        out.extend_from_slice(b"//");
        out.extend_from_slice(a);
    }
    if r.scheme.is_some() || r.authority.is_some() || r.path.first() == Some(&b'/') {
        remove_dot_segments(r.path, out);
    } else if r.path.is_empty() {
        out.extend_from_slice(b.path);
    } else {
        // Merge: the base path up to its last '/', then the reference's path.
        let mut merged = Vec::new();
        if b.authority.is_some() && b.path.is_empty() {
            merged.push(b'/');
        } else if let Some(i) = b.path.iter().rposition(|&c| c == b'/') {
            merged.extend_from_slice(&b.path[..=i]);
        }
        merged.extend_from_slice(r.path);
        remove_dot_segments(&merged, out);
    }
    if let Some(q) = query {
        out.push(b'?');
        out.extend_from_slice(q);
    }
    if let Some(f) = r.fragment {
        out.push(b'#');
        out.extend_from_slice(f);
    }
}
