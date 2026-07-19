//go:build linux && cgo

package main

/*
#cgo pkg-config: gtk+-3.0

#include <gtk/gtk.h>
#include <stdint.h>

extern void apexloadRunNativeUIOperation(uintptr_t operation_id);

typedef struct {
	GMutex mutex;
	GCond cond;
	gboolean done;
	uintptr_t operation_id;
} apexload_native_ui_state;

static gboolean apexload_run_native_ui_operation(gpointer opaque) {
	apexload_native_ui_state *state = (apexload_native_ui_state *)opaque;
	apexloadRunNativeUIOperation(state->operation_id);

	g_mutex_lock(&state->mutex);
	state->done = TRUE;
	g_cond_signal(&state->cond);
	g_mutex_unlock(&state->mutex);
	return G_SOURCE_REMOVE;
}

static int apexload_dispatch_native_ui_operation(uintptr_t operation_id) {
	GMainContext *context = g_main_context_default();
	if (g_main_context_is_owner(context)) {
		apexloadRunNativeUIOperation(operation_id);
		return 1;
	}

	apexload_native_ui_state state = {0};
	state.operation_id = operation_id;
	g_mutex_init(&state.mutex);
	g_cond_init(&state.cond);

	g_mutex_lock(&state.mutex);
	guint source_id = g_idle_add(apexload_run_native_ui_operation, &state);
	if (source_id != 0) {
		while (!state.done) {
			g_cond_wait(&state.cond, &state.mutex);
		}
	}
	g_mutex_unlock(&state.mutex);

	g_cond_clear(&state.cond);
	g_mutex_clear(&state.mutex);
	return source_id != 0;
}
*/
import "C"

import (
	"sync"
	"sync/atomic"
)

var (
	nextNativeUIOperationID atomic.Uint64
	nativeUIOperations      sync.Map
)

// dispatchNativeUI synchronously executes operation on GTK's main loop. Wails
// dispatches several Linux window APIs itself, but WindowSetSize and menu
// replacement still touch GTK directly in v2.13.
func dispatchNativeUI(operation func()) bool {
	if operation == nil {
		return true
	}
	id := nextNativeUIOperationID.Add(1)
	nativeUIOperations.Store(id, operation)
	if C.apexload_dispatch_native_ui_operation(C.uintptr_t(id)) == 0 {
		nativeUIOperations.Delete(id)
		return false
	}
	return true
}

//export apexloadRunNativeUIOperation
func apexloadRunNativeUIOperation(operationID C.uintptr_t) {
	operation, ok := nativeUIOperations.LoadAndDelete(uint64(operationID))
	if !ok {
		return
	}
	operation.(func())()
}
