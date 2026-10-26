//go:build wasip1

package fhr

import (
	"unsafe"
)

// The fhr-wasm-1 build (SPEC §7): GOOS=wasip1 GOARCH=wasm -buildmode=c-shared.
// The module exports fhr_alloc, fhr_free and fhr_call, and every request goes
// through Call, so this file only moves bytes across the module's memory.
//
// A c-shared build runs package initialisation in _initialize but never runs
// main, and a handler's main is where it hands its Handler to Run. So the
// first call runs main once (mainMain below): Run then records the handler
// and returns, and handlers keep their one-line main unchanged.

var (
	handler    Handler
	handlerInf Info
	mainRan    bool
	// Buffers handed to the host, by address, until it frees them. Go's
	// collector never moves memory, so an entry here keeps its bytes in place.
	pinned = map[uint32][]byte{}
)

// Run records the handler for the exports. Called from the handler's main,
// which the first fhr_call runs.
func Run(h Handler, info Info) {
	handler, handlerInf = h, info
}

//go:linkname mainMain main.main
func mainMain()

func pin(b []byte) uint32 {
	if len(b) == 0 {
		b = make([]byte, 1) // every buffer has an address, an empty one too
	}
	p := uint32(uintptr(unsafe.Pointer(&b[0])))
	pinned[p] = b
	return p
}

//go:wasmexport fhr_alloc
func fhrAlloc(size int32) int32 {
	if size < 0 {
		return 0
	}
	return int32(pin(make([]byte, size)))
}

//go:wasmexport fhr_free
func fhrFree(ptr, _ int32) {
	delete(pinned, uint32(ptr))
}

//go:wasmexport fhr_call
func fhrCall(ptr, size int32) int64 {
	if !mainRan {
		mainRan = true
		mainMain()
	}
	var resp []byte
	req, ok := pinned[uint32(ptr)]
	switch {
	case handler == nil:
		resp = encodeResponse(callResponse{Error: "the handler's main did not call fhr.Run"}, nil)
	case !ok || size < 0 || int(size) > len(req):
		resp = encodeResponse(callResponse{Error: "fhr_call: the request is not a buffer from fhr_alloc"}, nil)
	default:
		resp = Call(handler, handlerInf, req[:size])
	}
	return int64(pin(resp))<<32 | int64(len(resp))
}
