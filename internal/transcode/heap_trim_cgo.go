//go:build cgo

package transcode

/*
#include <stdlib.h>
#if defined(__GLIBC__)
#include <malloc.h>
#endif

static void streamly_trim_heap(void) {
#if defined(__GLIBC__)
	malloc_trim(0);
#endif
}
*/
import "C"

func trimNativeHeap() {

	C.streamly_trim_heap()

}
