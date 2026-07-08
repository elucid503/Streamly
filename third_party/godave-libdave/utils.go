package libdave

/*
#include <stdlib.h>
#include <stdint.h>

#ifdef _WIN32
#include <windows.h>

static void (*dave_free_fn)(void *) = NULL;
static int dave_free_resolved = 0;

static void resolve_dave_free(void) {
	if (dave_free_resolved) {
		return;
	}
	dave_free_resolved = 1;
	// libdave >= 1.1.1 exports daveFree so callers free with the DLL's CRT.
	// Prebuilt Windows binaries are MSVC; Go/cgo free() is MinGW — mixing crashes.
	HMODULE h = GetModuleHandleA("libdave.dll");
	if (h != NULL) {
		dave_free_fn = (void (*)(void *))GetProcAddress(h, "daveFree");
	}
}

static void free_dave_buf(void *p) {
	if (p == NULL) {
		return;
	}
	resolve_dave_free();
	if (dave_free_fn != NULL) {
		dave_free_fn(p);
		return;
	}
	// Older libdave: leaking is preferable to EXCEPTION_ACCESS_VIOLATION in free().
}
#else
static void free_dave_buf(void *p) {
	// Prefer daveFree when the install provides it (libdave >= 1.1.1).
	// The symbol is weakly resolved via the linked library when present.
	extern void daveFree(void *) __attribute__((weak));
	if (daveFree) {
		daveFree(p);
		return;
	}
	free(p);
}
#endif
*/
import "C"
import (
	"unsafe"
)

func stringSliceToC(strings []string) (**C.char, func()) {
	cArray := make([]*C.char, len(strings))
	for i, s := range strings {
		cArray[i] = C.CString(s)
	}

	freeFunc := func() {
		for _, ptr := range cArray {
			C.free(unsafe.Pointer(ptr))
		}
	}

	return &cArray[0], freeFunc
}

// IMPORTANT: This function will free the underlying C memory, so cArray becomes unsafe to use
// after this function call
func newByteSlice(cArray *C.uint8_t, length C.size_t) []byte {
	if cArray == nil || length == 0 {
		return nil
	}

	view := unsafe.Slice((*byte)(cArray), length)

	slice := make([]byte, length)
	copy(slice, view)

	C.free_dave_buf(unsafe.Pointer(cArray))

	return slice
}

// IMPORTANT: This function will free the underlying C memory, so cArray becomes unsafe to use
// after this function call
func newUint64Slice(cArray *C.uint64_t, length C.size_t) []uint64 {
	if cArray == nil || length == 0 {
		return nil
	}

	view := unsafe.Slice((*uint64)(cArray), length)

	slice := make([]uint64, length)
	copy(slice, view)

	C.free_dave_buf(unsafe.Pointer(cArray))

	return slice
}
