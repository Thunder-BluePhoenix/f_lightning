package search

// IndexHooks holds optional callbacks that customize the indexing pipeline per DocType.
type IndexHooks struct {
	// BeforeIndex: return false to skip indexing this document entirely.
	BeforeIndex func(doc map[string]interface{}) bool

	// TransformDoc: modify the document before it is sent to Meilisearch.
	TransformDoc func(doc map[string]interface{}) map[string]interface{}
}

// HookRegistry stores hooks keyed by Frappe DocType name.
type HookRegistry struct {
	m map[string]*IndexHooks
}

func newHookRegistry() *HookRegistry {
	r := &HookRegistry{m: make(map[string]*IndexHooks)}
	r.registerDefaults()
	return r
}

// Register sets hooks for a given DocType.  Safe to call before Start().
func (r *HookRegistry) Register(doctype string, hooks *IndexHooks) {
	r.m[doctype] = hooks
}

// registerDefaults wires built-in behaviours (e.g. skip cancelled docs).
func (r *HookRegistry) registerDefaults() {
	// Skip Draft (docstatus=0) and Cancelled (docstatus=2) for all transactional DocTypes.
	skipNonSubmitted := &IndexHooks{
		BeforeIndex: func(doc map[string]interface{}) bool {
			ds, ok := doc["docstatus"]
			if !ok {
				return true // No docstatus field — index it
			}
			switch v := ds.(type) {
			case int64:
				return v == 1
			case float64:
				return int(v) == 1
			case int:
				return v == 1
			}
			return true
		},
	}

	r.m["Sales Invoice"] = skipNonSubmitted
	r.m["Purchase Order"] = skipNonSubmitted
}

// runBeforeIndex returns true if the document should be indexed.
func (r *HookRegistry) runBeforeIndex(doctype string, doc map[string]interface{}) bool {
	hooks, ok := r.m[doctype]
	if !ok || hooks.BeforeIndex == nil {
		return true
	}
	return hooks.BeforeIndex(doc)
}

// runTransformDoc applies the transform hook if registered, or returns doc unchanged.
func (r *HookRegistry) runTransformDoc(doctype string, doc map[string]interface{}) map[string]interface{} {
	hooks, ok := r.m[doctype]
	if !ok || hooks.TransformDoc == nil {
		return doc
	}
	return hooks.TransformDoc(doc)
}
