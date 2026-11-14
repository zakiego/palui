package flows

// The step graph of a definition (graph.ts): edges, the default next,
// reachability and cycle detection.

type Edge struct {
	To    string
	Label string
	Body  bool
}

// DefaultNext is defaultNext: the step's next, else the following one, else "".
func DefaultNext(def *Definition, stepID string) *string {
	for i := range def.Steps {
		if def.Steps[i].ID != stepID {
			continue
		}
		s := def.Steps[i]
		if s.Next.Set {
			return s.Next.ID
		}
		if i+1 < len(def.Steps) {
			id := def.Steps[i+1].ID
			return &id
		}
		return nil
	}
	return nil
}

// EdgesOf is edgesOf.
func EdgesOf(def *Definition, s *Step) []Edge {
	var out []Edge
	add := func(to *string, label string, body bool) {
		if to != nil && *to != "" {
			out = append(out, Edge{To: *to, Label: label, Body: body})
		}
	}
	if s.Kind == "if" {
		add(s.Then.ID, "then", false)
		add(s.Else.ID, "else", false)
	} else {
		add(DefaultNext(def, s.ID), "next", false)
	}
	if s.Kind == "checkin" {
		if s.On != nil {
			for _, k := range s.On.Keys() {
				to, _ := s.On.Get(k)
				add(to, "on."+k, false)
			}
		}
		add(s.OnMissed.ID, "onMissed", false)
	}
	if s.Kind == "each" {
		body := s.Body
		add(&body, "body", true)
	}
	if s.OnError != nil && *s.OnError != "fail" && *s.OnError != "continue" {
		add(s.OnError, "onError", false)
	}
	return out
}

// Reachable is reachable: steps reachable from starts; viaBody false stops at
// each.body edges.
func Reachable(def *Definition, starts []string, viaBody bool) map[string]bool {
	seen := map[string]bool{}
	stack := append([]string{}, starts...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		s := def.Step(id)
		if seen[id] || s == nil {
			continue
		}
		seen[id] = true
		for _, e := range EdgesOf(def, s) {
			if viaBody || !e.Body {
				stack = append(stack, e.To)
			}
		}
	}
	return seen
}

// FindCycle is findCycle: a cycle as step ids, or nil.
func FindCycle(def *Definition) []string {
	state := map[string]int{}
	var path []string
	var visit func(id string) []string
	visit = func(id string) []string {
		switch state[id] {
		case 2:
			return nil
		case 1:
			for i, p := range path {
				if p == id {
					return append(append([]string{}, path[i:]...), id)
				}
			}
		}
		s := def.Step(id)
		if s == nil {
			return nil
		}
		state[id] = 1
		path = append(path, id)
		for _, e := range EdgesOf(def, s) {
			if c := visit(e.To); c != nil {
				return c
			}
		}
		path = path[:len(path)-1]
		state[id] = 2
		return nil
	}
	for i := range def.Steps {
		if c := visit(def.Steps[i].ID); c != nil {
			return c
		}
	}
	return nil
}

// StartOf is startOf: the trigger's start, else the first step.
func StartOf(def *Definition, triggerID string) string {
	var t *Trigger
	if triggerID != "" {
		t = def.Trigger(triggerID)
	} else if len(def.Triggers) > 0 {
		t = &def.Triggers[0]
	}
	if t != nil {
		return t.Start
	}
	return def.Steps[0].ID
}
