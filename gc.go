package lua

import "strings"

func (s *State) Retain(value Value) func() {
	if s.hostRoots == nil {
		s.hostRoots = make(map[uint64]Value)
	}
	s.rootID++
	id := s.rootID
	s.hostRoots[id] = value
	return func() { delete(s.hostRoots, id) }
}

type weakTable struct {
	table        *Table
	keys, values bool
}

type collector struct {
	marked map[any]bool
	queue  []any
	weak   []weakTable
}

func (t *thread) pendingCall() []Value {
	for index := len(t.frames) - 1; index >= 0; index-- {
		f := &t.frames[index]
		p := f.function.proto
		if p == nil {
			continue
		}
		pc := int(f.pc) - 1
		if pc < 0 || pc >= len(p.code) {
			return nil
		}
		site := p.instruction(pc)
		if site.op != opCall && site.op != opTailCall {
			return nil
		}
		start := int(f.base) + site.a
		end := start + 1 + site.b
		if site.b < 0 {
			end = t.top
		}
		if start < 0 || start >= len(t.stack) {
			return nil
		}
		return t.stack[start:max(start+1, min(end, len(t.stack)))]
	}
	return nil
}

func (g *collector) add(object any) {
	if !g.marked[object] {
		g.marked[object] = true
		g.queue = append(g.queue, object)
	}
}

func (g *collector) value(value Value) {
	switch value.Type() {
	case TypeTable:
		g.add(value.object.table)
	case TypeFunction:
		g.add(value.object.function)
	case TypeThread:
		g.add(value.object.thread)
	}
}

func (g *collector) dead(value Value) bool {
	switch value.Type() {
	case TypeTable:
		return !g.marked[value.object.table]
	case TypeFunction:
		return !g.marked[value.object.function]
	case TypeThread:
		return !g.marked[value.object.thread]
	}
	return false
}

func (p *Proto) pendingUnaryResults(pc, register int, pending *[256]bool) {
	if !p.separateCallResults {
		return
	}
	for next := pc + 1; next < len(p.code); next++ {
		i := p.instruction(next)
		if i.op == opPoll {
			continue
		}
		if i.op != opUnary || i.b != register {
			return
		}
		pending[i.a] = true
		register = i.a
	}
}

func (g *collector) stack(t *thread) {
	for index := range t.frames {
		f := &t.frames[index]
		g.add(f.function)
		if f.function.proto == nil {
			if f.pc == 0 {
				for _, value := range t.stack[f.start:f.base] {
					g.value(value)
				}
			}
			continue
		}
		p := f.function.proto
		limit := int(f.base) + p.maxRegisters
		resultStart, resultEnd := -1, -1
		var pendingUnary [256]bool
		pc := int(f.pc) - 1
		if pc >= 0 && pc < len(p.code) {
			site := p.instruction(pc)
			if site.op == opCall || site.op == opTailCall {
				limit = int(f.base) + site.a + 1
				resultStart = int(f.base) + site.d
				resultEnd = resultStart + max(1, site.c)
				if site.c < 0 {
					resultEnd = limit - 1
				}
				p.pendingUnaryResults(pc, site.d, &pendingUnary)
			}
		}
		for slot := int(f.start); slot < min(limit, len(t.stack)); slot++ {
			register := slot - int(f.base)
			if slot < limit-1 && (slot >= resultStart && slot < resultEnd || register >= 0 && pendingUnary[register]) {
				continue
			}
			g.value(t.stack[slot])
		}
	}
	for _, root := range t.nativeRoots {
		for _, value := range root.values {
			g.value(value)
		}
	}
	for _, value := range t.hookRoots {
		g.value(value)
	}
	for u := t.open; u != nil; u = u.next {
		g.value(u.get())
	}
	if t.handlerActive {
		g.value(t.handler)
	}
	for _, handler := range t.handlerRoots {
		g.value(handler)
	}
}

func (g *collector) table(t *Table) {
	keys, values := false, false
	if t.metatable != nil {
		g.add(t.metatable)
		if mode := t.metatable.RawGet(String("__mode")); mode.Type() == TypeString {
			text := mode.Text()
			if end := strings.IndexByte(text, 0); end >= 0 {
				text = text[:end]
			}
			keys = strings.ContainsRune(text, 'k')
			values = strings.ContainsRune(text, 'v')
		}
	}
	if keys || values {
		g.weak = append(g.weak, weakTable{t, keys, values})
	}
	if !values {
		for _, value := range t.array {
			g.value(value)
		}
	}
	for _, node := range t.nodes {
		if node.value.IsNil() {
			continue
		}
		if !keys {
			g.value(node.key)
		}
		if !values {
			g.value(node.value)
		}
	}
}

func (s *State) collect() {
	g := collector{marked: make(map[any]bool)}
	g.add(s.globals)
	g.add(s.stringMeta)
	g.add(s.main)
	g.add(s.current)
	for _, value := range s.hostRoots {
		g.value(value)
	}
	for len(g.queue) > 0 {
		last := len(g.queue) - 1
		object := g.queue[last]
		g.queue = g.queue[:last]
		switch object := object.(type) {
		case *Table:
			g.table(object)
		case *function:
			if object.env != nil {
				g.add(object.env)
			}
			for _, u := range object.upvalues {
				g.value(u.get())
			}
		case *thread:
			if object.parent != nil {
				g.add(object.parent)
			}
			g.value(object.entry)
			g.stack(object)
		}
	}
	for _, weak := range g.weak {
		t := weak.table
		if weak.values {
			for index, value := range t.array {
				if g.dead(value) {
					t.setArray(index, Nil())
					t.version++
				}
			}
		}
		for index := range t.nodes {
			node := &t.nodes[index]
			if !node.value.IsNil() && (weak.keys && g.dead(node.key) || weak.values && g.dead(node.value)) {
				node.value = Nil()
				t.version++
			}
		}
	}
}
