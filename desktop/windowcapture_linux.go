//go:build linux && cgo

package main

/*
#cgo pkg-config: gtk+-3.0

#include <gtk/gtk.h>

typedef struct {
	int width;
	int height;
	int x;
	int y;
	int maximised;
	int valid;
} apexload_window_geometry;

typedef struct {
	GMutex mutex;
	GCond cond;
	gboolean done;
	apexload_window_geometry geometry;
} apexload_capture_state;

static GtkWindow *apexload_find_window(void) {
	GList *windows = gtk_window_list_toplevels();
	GtkWindow *fallback = NULL;
	for (GList *item = windows; item != NULL; item = item->next) {
		if (!GTK_IS_WINDOW(item->data) || !gtk_widget_get_visible(GTK_WIDGET(item->data))) {
			continue;
		}
		GtkWindow *candidate = GTK_WINDOW(item->data);
		if (fallback == NULL) {
			fallback = candidate;
		}
		if (gtk_window_is_active(candidate)) {
			fallback = candidate;
			break;
		}
	}
	g_list_free(windows);
	return fallback;
}

static gboolean apexload_capture_window_geometry_on_main(gpointer opaque) {
	apexload_capture_state *state = (apexload_capture_state *)opaque;
	GtkWindow *window = apexload_find_window();
	int width = 0;
	int height = 0;
	int x = 0;
	int y = 0;
	int maximised = 0;
	if (window != NULL) {
		gtk_window_get_size(window, &width, &height);
		gtk_window_get_position(window, &x, &y);
		GdkWindow *gdk_window = gtk_widget_get_window(GTK_WIDGET(window));
		if (gdk_window != NULL) {
			maximised = (gdk_window_get_state(gdk_window) & GDK_WINDOW_STATE_MAXIMIZED) != 0;
		}
	}

	g_mutex_lock(&state->mutex);
	state->geometry.width = width;
	state->geometry.height = height;
	state->geometry.x = x;
	state->geometry.y = y;
	state->geometry.maximised = maximised;
	state->geometry.valid = window != NULL && width > 0 && height > 0;
	state->done = TRUE;
	g_cond_signal(&state->cond);
	g_mutex_unlock(&state->mutex);
	return G_SOURCE_REMOVE;
}

static apexload_window_geometry apexload_capture_window_geometry(void) {
	apexload_capture_state state = {0};
	g_mutex_init(&state.mutex);
	g_cond_init(&state.cond);

	GMainContext *context = g_main_context_default();
	if (g_main_context_is_owner(context)) {
		apexload_capture_window_geometry_on_main(&state);
	} else {
		g_mutex_lock(&state.mutex);
		guint source_id = g_idle_add(apexload_capture_window_geometry_on_main, &state);
		if (source_id != 0) {
			while (!state.done) {
				g_cond_wait(&state.cond, &state.mutex);
			}
		}
		g_mutex_unlock(&state.mutex);
	}

	g_cond_clear(&state.cond);
	g_mutex_clear(&state.mutex);
	return state.geometry;
}
*/
import "C"

import "context"

// Wails invokes Linux OnBeforeClose on a goroutine, while GTK window state may
// only be read on its main loop. Dispatch one synchronous snapshot through the
// GTK idle queue before Wails destroys the native window.
func captureNativeWindowGeometry(ctx context.Context) (nativeWindowGeometry, bool) {
	if ctx == nil {
		return nativeWindowGeometry{}, false
	}
	geometry := C.apexload_capture_window_geometry()
	if geometry.valid == 0 {
		return nativeWindowGeometry{}, false
	}
	return nativeWindowGeometry{
		Width:     int(geometry.width),
		Height:    int(geometry.height),
		X:         int(geometry.x),
		Y:         int(geometry.y),
		Maximised: geometry.maximised != 0,
	}, true
}
