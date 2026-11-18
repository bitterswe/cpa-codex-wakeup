package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct { void *ptr; size_t len; } Buffer;
typedef struct {
	uint32_t abi_version;
	void *context;
	int (*call)(void *, const char *, const uint8_t *, size_t, Buffer *);
	void (*release)(void *, size_t);
} Host;
typedef struct {
	uint32_t abi_version;
	int (*call)(char *, uint8_t *, size_t, Buffer *);
	void (*release)(void *, size_t);
	void (*shutdown)(void);
} Plugin;

extern int wakeupCall(char *, uint8_t *, size_t, Buffer *);
extern void wakeupRelease(void *, size_t);
extern void wakeupShutdown(void);
static inline void bindPlugin(Plugin *p) {
	p->abi_version = 1;
	p->call = wakeupCall;
	p->release = wakeupRelease;
	p->shutdown = wakeupShutdown;
}
*/
import "C"

import (
	"encoding/json"
	"unsafe"
)

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.Host, plugin *C.Plugin) C.int {
	if host == nil || host.abi_version != 1 || plugin == nil {
		return 1
	}
	C.bindPlugin(plugin)
	return 0
}

//export wakeupCall
func wakeupCall(method *C.char, input *C.uint8_t, length C.size_t, out *C.Buffer) C.int {
	if out == nil {
		return 1
	}
	out.ptr, out.len = nil, 0
	if method == nil || (input == nil && length != 0) || length > 1<<31-1 {
		return 1
	}
	response := handle(C.GoString(method), C.GoBytes(unsafe.Pointer(input), C.int(length)))
	data, err := json.Marshal(response)
	if err != nil {
		return 1
	}
	out.ptr, out.len = C.CBytes(data), C.size_t(len(data))
	return 0
}

//export wakeupRelease
func wakeupRelease(ptr unsafe.Pointer, _ C.size_t) { C.free(ptr) }

//export wakeupShutdown
func wakeupShutdown() {}

func main() {}
