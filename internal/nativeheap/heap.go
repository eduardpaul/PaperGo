// Package nativeheap reduces allocator retention for PaperGo's native SDKs.
// Go's garbage collector cannot reclaim memory retained by the C allocator.
package nativeheap

/*
#include <stdlib.h>
#if defined(__GLIBC__)
#include <malloc.h>
#elif defined(__APPLE__)
#include <malloc/malloc.h>
#endif

static void papergo_configure_heap(void) {
#if defined(__GLIBC__)
    // Bound per-thread arenas and keep large temporary buffers eligible for
    // munmap, instead of glibc raising its thresholds after indexing bursts.
    mallopt(M_ARENA_MAX, 2);
    mallopt(M_TRIM_THRESHOLD, 128 * 1024);
#endif
}

static void papergo_release_heap(void) {
#if defined(__GLIBC__)
    malloc_trim(0);
#elif defined(__APPLE__)
    malloc_zone_pressure_relief(NULL, 0);
#endif
}
*/
import "C"

func init() { C.papergo_configure_heap() }

// Release returns unused allocator pages after native maintenance or unloading
// idle models. It leaves live allocations intact. Windows uses its CRT policy.
func Release() { C.papergo_release_heap() }
