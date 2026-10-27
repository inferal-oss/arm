//! XPath regular expressions (REGEX, REPLACE) as a backtracking VM over code points.
//! Flags: i (case-insensitive), s (dot matches newlines), m (multi-line anchors),
//! x (ignore white space in the pattern), q (the pattern is literal).

use alloc::vec::Vec;

#[derive(Clone)]
enum Item {
    Range(u32, u32),
    Digit(bool),
    Space(bool),
    Word(bool),
}

#[derive(Clone)]
struct Class {
    items: Vec<Item>,
    negated: bool,
}

#[derive(Clone)]
enum Inst {
    Char(u32),
    Any,
    Class(usize),
    Start,
    End,
    Split(usize, usize),
    Jmp(usize),
    Save(usize),
    /// Loop guard: remember the position in register `r`...
    Mark(usize),
    /// ...and fail if a loop iteration consumed nothing since.
    Progress(usize),
    Backref(usize),
    Match,
}

/// A parsed pattern, before compilation.
#[derive(Clone)]
enum Node {
    Char(u32),
    Any,
    Class(usize),
    Start,
    End,
    Group(alloc::boxed::Box<Node>, Option<usize>),
    Concat(Vec<Node>),
    Alt(Vec<Node>),
    Repeat(alloc::boxed::Box<Node>, u32, Option<u32>, bool),
    Backref(usize),
}

pub struct Regex {
    prog: Vec<Inst>,
    classes: Vec<Class>,
    caps: usize,
    loops: usize,
    icase: bool,
    multiline: bool,
    dotall: bool,
}

pub fn chars(s: &[u8]) -> Vec<u32> {
    let mut out = Vec::with_capacity(s.len());
    let mut i = 0;
    while i < s.len() {
        let b = s[i] as u32;
        let (c, n) = if b < 0x80 {
            (b, 1)
        } else if b < 0xe0 {
            ((b & 0x1f) << 6 | (s.get(i + 1).copied().unwrap_or(0) as u32 & 0x3f), 2)
        } else if b < 0xf0 {
            ((b & 0x0f) << 12 | (s.get(i + 1).copied().unwrap_or(0) as u32 & 0x3f) << 6 | (s.get(i + 2).copied().unwrap_or(0) as u32 & 0x3f), 3)
        } else {
            (
                (b & 0x07) << 18
                    | (s.get(i + 1).copied().unwrap_or(0) as u32 & 0x3f) << 12
                    | (s.get(i + 2).copied().unwrap_or(0) as u32 & 0x3f) << 6
                    | (s.get(i + 3).copied().unwrap_or(0) as u32 & 0x3f),
                4,
            )
        };
        out.push(c);
        i += n;
    }
    out
}

fn lower(c: u32) -> u32 {
    char::from_u32(c).map_or(c, |ch| ch.to_lowercase().next().unwrap_or(ch) as u32)
}

fn is_space(c: u32) -> bool {
    matches!(c, 0x20 | 0x09 | 0x0a | 0x0d)
}

fn is_word(c: u32) -> bool {
    match char::from_u32(c) {
        Some(ch) if ch.is_alphanumeric() => true,
        Some(ch) if (ch as u32) < 0x80 => b"$+<=>^`|~".contains(&(ch as u8)),
        _ => false,
    }
}

struct Parser<'a> {
    p: &'a [u32],
    i: usize,
    classes: Vec<Class>,
    caps: usize,
}

impl Parser<'_> {
    fn peek(&self) -> Option<u32> {
        self.p.get(self.i).copied()
    }

    fn alt(&mut self) -> Option<Node> {
        let mut branches = alloc::vec![self.concat()?];
        while self.peek() == Some('|' as u32) {
            self.i += 1;
            branches.push(self.concat()?);
        }
        Some(if branches.len() == 1 { branches.pop().unwrap() } else { Node::Alt(branches) })
    }

    fn concat(&mut self) -> Option<Node> {
        let mut items = Vec::new();
        while let Some(c) = self.peek() {
            if c == '|' as u32 || c == ')' as u32 {
                break;
            }
            let atom = self.atom()?;
            items.push(self.quantified(atom)?);
        }
        Some(Node::Concat(items))
    }

    fn number(&mut self) -> Option<u32> {
        let start = self.i;
        let mut n: u32 = 0;
        while let Some(c @ 0x30..=0x39) = self.peek() {
            n = n.checked_mul(10)?.checked_add(c - 0x30)?;
            self.i += 1;
        }
        if self.i == start { None } else { Some(n) }
    }

    fn quantified(&mut self, atom: Node) -> Option<Node> {
        let (min, max) = match self.peek() {
            Some(0x2a) => (0, None),
            Some(0x2b) => (1, None),
            Some(0x3f) => (0, Some(1)),
            Some(0x7b) => {
                self.i += 1;
                let min = self.number()?;
                let max = if self.peek() == Some(',' as u32) {
                    self.i += 1;
                    if self.peek() == Some('}' as u32) { None } else { Some(self.number()?) }
                } else {
                    Some(min)
                };
                if self.peek() != Some('}' as u32) || max.is_some_and(|m| m < min) {
                    return None;
                }
                self.i -= 0;
                (min, max)
            }
            _ => return Some(atom),
        };
        self.i += 1;
        if matches!(atom, Node::Start | Node::End) {
            return None;
        }
        let greedy = if self.peek() == Some('?' as u32) {
            self.i += 1;
            false
        } else {
            true
        };
        Some(Node::Repeat(alloc::boxed::Box::new(atom), min, max, greedy))
    }

    fn atom(&mut self) -> Option<Node> {
        let c = self.peek()?;
        self.i += 1;
        Some(match char::from_u32(c)? {
            '.' => Node::Any,
            '^' => Node::Start,
            '$' => Node::End,
            '(' => {
                let cap = if self.p[self.i..].starts_with(&['?' as u32, ':' as u32]) {
                    self.i += 2;
                    None
                } else {
                    self.caps += 1;
                    Some(self.caps)
                };
                let inner = self.alt()?;
                if self.peek() != Some(')' as u32) {
                    return None;
                }
                self.i += 1;
                Node::Group(alloc::boxed::Box::new(inner), cap)
            }
            '[' => self.class()?,
            '\\' => self.escape(false)?.0,
            '*' | '+' | '?' | '{' | ')' | ']' | '}' => return None,
            _ => Node::Char(c),
        })
    }

    /// An escape after `\`: a node, and the single char it denotes if any (for ranges).
    fn escape(&mut self, in_class: bool) -> Option<(Node, Option<u32>)> {
        let c = char::from_u32(self.peek()?)?;
        self.i += 1;
        let single = |ch: u32| Some((Node::Char(ch), Some(ch)));
        let class = |me: &mut Self, item: Item| {
            me.classes.push(Class { items: alloc::vec![item], negated: false });
            Some((Node::Class(me.classes.len() - 1), None))
        };
        match c {
            'n' => single(0x0a),
            'r' => single(0x0d),
            't' => single(0x09),
            '\\' | '|' | '.' | '-' | '^' | '?' | '*' | '+' | '{' | '}' | '(' | ')' | '[' | ']' | '$' | '/' => single(c as u32),
            'd' => class(self, Item::Digit(false)),
            'D' => class(self, Item::Digit(true)),
            's' => class(self, Item::Space(false)),
            'S' => class(self, Item::Space(true)),
            'w' => class(self, Item::Word(false)),
            'W' => class(self, Item::Word(true)),
            '1'..='9' if !in_class => Some((Node::Backref(c as usize - '0' as usize), None)),
            _ => None,
        }
    }

    fn class(&mut self) -> Option<Node> {
        let negated = self.peek() == Some('^' as u32);
        if negated {
            self.i += 1;
        }
        let mut items = Vec::new();
        let mut first = true;
        loop {
            let c = self.peek()?;
            if c == ']' as u32 && !first {
                self.i += 1;
                break;
            }
            first = false;
            self.i += 1;
            let lo = if c == '\\' as u32 {
                match self.escape(true)? {
                    (_, Some(ch)) => ch,
                    (Node::Class(k), None) => {
                        let item = self.classes[k].items[0].clone();
                        items.push(item);
                        continue;
                    }
                    _ => return None,
                }
            } else {
                c
            };
            if self.peek() == Some('-' as u32) && self.p.get(self.i + 1).is_some_and(|&n| n != ']' as u32) {
                self.i += 1;
                let mut hi = self.peek()?;
                self.i += 1;
                if hi == '\\' as u32 {
                    hi = self.escape(true)?.1?;
                }
                if hi < lo {
                    return None;
                }
                items.push(Item::Range(lo, hi));
            } else {
                items.push(Item::Range(lo, lo));
            }
        }
        self.classes.push(Class { items, negated });
        Some(Node::Class(self.classes.len() - 1))
    }
}

struct Compiler {
    prog: Vec<Inst>,
    loops: usize,
}

impl Compiler {
    fn emit(&mut self, i: Inst) -> usize {
        self.prog.push(i);
        self.prog.len() - 1
    }

    fn node(&mut self, n: &Node) {
        match n {
            Node::Char(c) => {
                self.emit(Inst::Char(*c));
            }
            Node::Any => {
                self.emit(Inst::Any);
            }
            Node::Class(k) => {
                self.emit(Inst::Class(*k));
            }
            Node::Start => {
                self.emit(Inst::Start);
            }
            Node::End => {
                self.emit(Inst::End);
            }
            Node::Backref(k) => {
                self.emit(Inst::Backref(*k));
            }
            Node::Group(inner, cap) => {
                if let Some(k) = cap {
                    self.emit(Inst::Save(2 * k));
                }
                self.node(inner);
                if let Some(k) = cap {
                    self.emit(Inst::Save(2 * k + 1));
                }
            }
            Node::Concat(items) => items.iter().for_each(|x| self.node(x)),
            Node::Alt(branches) => {
                let mut jumps = Vec::new();
                for (i, b) in branches.iter().enumerate() {
                    if i + 1 < branches.len() {
                        let split = self.emit(Inst::Split(0, 0));
                        self.node(b);
                        jumps.push(self.emit(Inst::Jmp(0)));
                        let next = self.prog.len();
                        self.prog[split] = Inst::Split(split + 1, next);
                    } else {
                        self.node(b);
                    }
                }
                let end = self.prog.len();
                for j in jumps {
                    self.prog[j] = Inst::Jmp(end);
                }
            }
            Node::Repeat(inner, min, max, greedy) => {
                for _ in 0..*min {
                    self.node(inner);
                }
                match max {
                    None => {
                        // L: split(body, out); body; progress; jmp L
                        let r = self.loops;
                        self.loops += 1;
                        let split = self.emit(Inst::Split(0, 0));
                        self.emit(Inst::Mark(r));
                        self.node(inner);
                        self.emit(Inst::Progress(r));
                        self.emit(Inst::Jmp(split));
                        let out = self.prog.len();
                        self.prog[split] = if *greedy { Inst::Split(split + 1, out) } else { Inst::Split(out, split + 1) };
                    }
                    Some(max) => {
                        let mut splits = Vec::new();
                        for _ in *min..*max {
                            splits.push(self.emit(Inst::Split(0, 0)));
                            self.node(inner);
                        }
                        let out = self.prog.len();
                        for s in splits {
                            self.prog[s] = if *greedy { Inst::Split(s + 1, out) } else { Inst::Split(out, s + 1) };
                        }
                    }
                }
            }
        }
    }
}

pub fn compile(pattern: &[u8], flags: &[u8]) -> Option<Regex> {
    let mut p = chars(pattern);
    let (mut icase, mut multiline, mut dotall) = (false, false, false);
    for &f in flags {
        match f {
            b'i' => icase = true,
            b'm' => multiline = true,
            b's' => dotall = true,
            b'x' => p.retain(|&c| !is_space(c)),
            b'q' => {}
            _ => return None,
        }
    }
    let node = if flags.contains(&b'q') {
        Node::Concat(p.iter().map(|&c| Node::Char(c)).collect())
    } else {
        let mut parser = Parser { p: &p, i: 0, classes: Vec::new(), caps: 0 };
        let n = parser.alt()?;
        if parser.i != p.len() {
            return None;
        }
        let mut c = Compiler { prog: Vec::new(), loops: 0 };
        c.emit(Inst::Save(0));
        c.node(&n);
        c.emit(Inst::Save(1));
        c.emit(Inst::Match);
        return Some(Regex { prog: c.prog, classes: parser.classes, caps: parser.caps + 1, loops: c.loops, icase, multiline, dotall });
    };
    let mut c = Compiler { prog: Vec::new(), loops: 0 };
    c.emit(Inst::Save(0));
    c.node(&node);
    c.emit(Inst::Save(1));
    c.emit(Inst::Match);
    Some(Regex { prog: c.prog, classes: Vec::new(), caps: 1, loops: 0, icase, multiline, dotall })
}

impl Regex {
    fn class_matches(&self, k: usize, c: u32) -> bool {
        let class = &self.classes[k];
        let test = |c: u32| {
            class.items.iter().any(|it| match *it {
                Item::Range(lo, hi) => lo <= c && c <= hi,
                Item::Digit(neg) => (0x30..=0x39).contains(&c) != neg,
                Item::Space(neg) => is_space(c) != neg,
                Item::Word(neg) => is_word(c) != neg,
            })
        };
        let hit = test(c) || self.icase && (test(lower(c)) || char::from_u32(c).is_some_and(|ch| ch.to_uppercase().any(|u| test(u as u32))));
        hit != class.negated
    }

    /// Match at `start`: capture positions (char indices), slot 2k/2k+1 for group k.
    fn run(&self, text: &[u32], start: usize, slots: &mut Vec<usize>) -> bool {
        const NONE: usize = usize::MAX;
        let nslots = self.caps * 2 + self.loops;
        let mut regs = alloc::vec![NONE; nslots];
        // Backtracking stack: (pc, sp, saved register index, old value) or a choice point.
        enum Frame {
            Try(usize, usize),
            Restore(usize, usize),
        }
        let mut stack: Vec<Frame> = alloc::vec![Frame::Try(0, start)];
        let mut steps: u32 = 0;
        while let Some(f) = stack.pop() {
            let (mut pc, mut sp) = match f {
                Frame::Restore(r, v) => {
                    regs[r] = v;
                    continue;
                }
                Frame::Try(pc, sp) => (pc, sp),
            };
            loop {
                steps += 1;
                if steps > 5_000_000 {
                    return false;
                }
                match self.prog[pc] {
                    Inst::Char(c) => {
                        let ok = text.get(sp).is_some_and(|&t| t == c || self.icase && lower(t) == lower(c));
                        if !ok {
                            break;
                        }
                        sp += 1;
                        pc += 1;
                    }
                    Inst::Any => {
                        if !text.get(sp).is_some_and(|&t| self.dotall || t != 0x0a && t != 0x0d) {
                            break;
                        }
                        sp += 1;
                        pc += 1;
                    }
                    Inst::Class(k) => {
                        if !text.get(sp).is_some_and(|&t| self.class_matches(k, t)) {
                            break;
                        }
                        sp += 1;
                        pc += 1;
                    }
                    Inst::Start => {
                        if !(sp == 0 || self.multiline && text[sp - 1] == 0x0a) {
                            break;
                        }
                        pc += 1;
                    }
                    Inst::End => {
                        if !(sp == text.len() || self.multiline && text[sp] == 0x0a) {
                            break;
                        }
                        pc += 1;
                    }
                    Inst::Split(a, b) => {
                        stack.push(Frame::Try(b, sp));
                        pc = a;
                    }
                    Inst::Jmp(a) => pc = a,
                    Inst::Save(r) | Inst::Mark(r) => {
                        let r = if let Inst::Mark(_) = self.prog[pc] { self.caps * 2 + r } else { r };
                        stack.push(Frame::Restore(r, regs[r]));
                        regs[r] = sp;
                        pc += 1;
                    }
                    Inst::Progress(r) => {
                        if regs[self.caps * 2 + r] == sp {
                            break;
                        }
                        pc += 1;
                    }
                    Inst::Backref(k) => {
                        let (a, b) = (regs.get(2 * k).copied().unwrap_or(NONE), regs.get(2 * k + 1).copied().unwrap_or(NONE));
                        if a == NONE || b == NONE {
                            pc += 1;
                            continue;
                        }
                        let n = b - a;
                        if sp + n > text.len() || text[a..b] != text[sp..sp + n] {
                            break;
                        }
                        sp += n;
                        pc += 1;
                    }
                    Inst::Match => {
                        slots.clear();
                        slots.extend_from_slice(&regs[..self.caps * 2]);
                        return true;
                    }
                }
            }
        }
        false
    }

    /// The first match at or after `from`: capture slots (usize::MAX when unset).
    pub fn find(&self, text: &[u32], from: usize) -> Option<Vec<usize>> {
        let mut slots = Vec::new();
        for start in from..=text.len() {
            if self.run(text, start, &mut slots) {
                return Some(slots);
            }
        }
        None
    }
}
