package mcp

import (
	"context"
	"sync"

	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// structureBase is what every structure call reads before it looks at the
// folder it was asked about: the file list, the relationships summed by
// directory, and the files a test reaches. None of it depends on the folder,
// only on the index run and the confidence floor, and on a large index it is
// most of the cost of a call (about half a second on 27,000 files). The web portal
// asks structure for one folder after another as a person drills in, so the
// base is kept for the run and floor and every later folder is a walk over
// memory. A new run is a new key; the old entries are dropped.
type structureBase struct {
	files    []registry.FolderFile
	dirEdges []dirPairEdges
	reached  []int64
}

type structureKey struct {
	srv   *Server
	runID int64
	floor model.Confidence
}

type structureEntry struct {
	once sync.Once
	base *structureBase
	err  error
}

var (
	structureMu    sync.Mutex
	structureMemo  = map[structureKey]*structureEntry{}
	structureOrder []structureKey
)

// structureMemoMax bounds the entries kept: a few floors of the current run.
const structureMemoMax = 8

// loadStructureBase returns the memoised base for the run and floor, reading
// it once. A failed read is not kept, so the next call tries again.
func (s *Server) loadStructureBase(ctx context.Context, runID int64, floor model.Confidence) (*structureBase, error) {
	key := structureKey{srv: s, runID: runID, floor: floor}
	structureMu.Lock()
	e := structureMemo[key]
	if e == nil {
		e = &structureEntry{}
		structureMemo[key] = e
		structureOrder = append(structureOrder, key)
		for len(structureOrder) > structureMemoMax {
			delete(structureMemo, structureOrder[0])
			structureOrder = structureOrder[1:]
		}
	}
	structureMu.Unlock()

	e.once.Do(func() { e.base, e.err = s.readStructureBase(ctx, runID, floor) })
	if e.err != nil {
		structureMu.Lock()
		if structureMemo[key] == e {
			delete(structureMemo, key)
		}
		structureMu.Unlock()
		return nil, e.err
	}
	return e.base, nil
}

func (s *Server) readStructureBase(ctx context.Context, runID int64, floor model.Confidence) (*structureBase, error) {
	files, err := s.deps.Registry.FolderFiles(ctx, runID)
	if err != nil {
		return nil, structureErr{"read the file list", err}
	}
	fileEdges, err := s.deps.Registry.FileEdgeRollup(ctx, runID, structureKinds, floor)
	if err != nil {
		return nil, structureErr{"sum the relationships by folder", err}
	}
	var testIDs []int64
	for _, f := range files {
		if capability.IsTestPath(f.Path) {
			testIDs = append(testIDs, f.ID)
		}
	}
	reached, err := s.deps.Registry.FilesReachedFrom(ctx, runID, testIDs, structureKinds, floor)
	if err != nil {
		return nil, structureErr{"find the files tests reach", err}
	}
	return &structureBase{files: files, dirEdges: foldByDirectory(fileEdges, files), reached: reached}, nil
}

// structureErr names the step of the base read that failed.
type structureErr struct {
	step string
	err  error
}

func (e structureErr) Error() string { return e.step + ": " + e.err.Error() }
func (e structureErr) Unwrap() error { return e.err }

// endpointMemo keeps the endpoint report of the current run. The report is a
// scan of every route the index recorded (about half a second on a large
// index) and the list is read a page at a time, so without it every page of
// one list paid for the whole scan again.
type endpointKey struct {
	srv   *Server
	runID int64
}

type endpointEntry struct {
	once sync.Once
	list []registry.Endpoint
	err  error
}

var (
	endpointMu   sync.Mutex
	endpointMemo = map[endpointKey]*endpointEntry{}
)

// endpointReport is Registry.EndpointReport for the run, read once. Callers
// must not modify the slice it returns.
func (s *Server) endpointReport(ctx context.Context, runID int64) ([]registry.Endpoint, error) {
	key := endpointKey{srv: s, runID: runID}
	endpointMu.Lock()
	e := endpointMemo[key]
	if e == nil {
		// one run per server at a time: drop the entries of older runs
		for k := range endpointMemo {
			if k.srv == s {
				delete(endpointMemo, k)
			}
		}
		e = &endpointEntry{}
		endpointMemo[key] = e
	}
	endpointMu.Unlock()
	e.once.Do(func() { e.list, e.err = s.deps.Registry.EndpointReport(ctx, runID) })
	if e.err != nil {
		endpointMu.Lock()
		if endpointMemo[key] == e {
			delete(endpointMemo, key)
		}
		endpointMu.Unlock()
		return nil, e.err
	}
	return e.list, nil
}

// configMemo keeps the settings-key report of the current run, for the same
// reason as endpointMemo: the list is read a page at a time (a large project
// has tens of thousands of keys), and each page re-ran the whole scan.
type configEntry struct {
	once sync.Once
	list []registry.ConfigKeyUsage
	err  error
}

var (
	configMu   sync.Mutex
	configMemo = map[endpointKey]*configEntry{}
)

// configReport is Registry.ConfigReport for the run, read once. Callers must
// not modify the slice it returns.
func (s *Server) configReport(ctx context.Context, runID int64) ([]registry.ConfigKeyUsage, error) {
	key := endpointKey{srv: s, runID: runID}
	configMu.Lock()
	e := configMemo[key]
	if e == nil {
		for k := range configMemo {
			if k.srv == s {
				delete(configMemo, k)
			}
		}
		e = &configEntry{}
		configMemo[key] = e
	}
	configMu.Unlock()
	e.once.Do(func() { e.list, e.err = s.deps.Registry.ConfigReport(ctx, runID) })
	if e.err != nil {
		configMu.Lock()
		if configMemo[key] == e {
			delete(configMemo, key)
		}
		configMu.Unlock()
		return nil, e.err
	}
	return e.list, nil
}
