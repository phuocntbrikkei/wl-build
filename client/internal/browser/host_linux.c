// Phần GTK/WebKitGTK của trình duyệt tích hợp (Linux). Xem host_linux.go.
#include <stdlib.h>
#include <gtk/gtk.h>
#include <gdk/gdkkeysyms.h>
#include <webkit2/webkit2.h>

// Hàm Go export (host_linux.go)
extern int  rkDecide(char *uri, int mainFrame);
extern void rkNewWindow(char *uri);
extern void rkCommitted(char *uri);
extern void rkLoaded(char *uri);
extern void rkTitle(char *title);
extern void rkHistory(int back, int fwd);
extern void rkToolbarMsg(char *msg);
extern void rkRunQueue(void);
extern int  rkKey(int key, int ctrl, int alt);

static GtkWidget *rkWin, *rkToolbar, *rkContent;

static char *rk_request_uri(WebKitNavigationAction *a) {
	WebKitURIRequest *r = webkit_navigation_action_get_request(a);
	return (char *)webkit_uri_request_get_uri(r);
}

// Khung chính hay khung con? WebKitGTK (bản GTK3) không cho biết trực tiếp.
// Coi là khung chính: lượt người dùng thật sự bấm/gõ, lùi-tới/tải lại, lượt do
// chính trình duyệt gọi load_uri, và chuyển hướng xảy ra khi khung chính đang tải
// dở (trước COMMITTED — lúc đó trang chưa có iframe nào). Còn lại (iframe quảng
// cáo, đo lường, script tự chuyển trang) coi là khung con: bị chặn thì bỏ qua lặng
// lẽ. Iframe quảng cáo hay tự gửi form (FORM_SUBMITTED) nên phải dựa vào "người
// dùng thao tác" chứ không dựa vào loại điều hướng. Khung chính lọt qua vẫn bị
// bắt lại ở LOAD_COMMITTED (Core.Committed).
static int rk_programmatic = 0;
static int rk_provisional = 0; // khung chính đang tải dở (STARTED/REDIRECTED, chưa COMMITTED)

static gboolean rk_decide_policy(WebKitWebView *v, WebKitPolicyDecision *d, WebKitPolicyDecisionType t, gpointer u) {
	if (t == WEBKIT_POLICY_DECISION_TYPE_NAVIGATION_ACTION) {
		WebKitNavigationAction *a = webkit_navigation_policy_decision_get_navigation_action(WEBKIT_NAVIGATION_POLICY_DECISION(d));
		WebKitNavigationType nt = webkit_navigation_action_get_navigation_type(a);
		int main = rk_programmatic || webkit_navigation_action_is_user_gesture(a) ||
		           nt == WEBKIT_NAVIGATION_TYPE_BACK_FORWARD || nt == WEBKIT_NAVIGATION_TYPE_RELOAD ||
		           (webkit_navigation_action_is_redirect(a) && rk_provisional);
		rk_programmatic = 0;
		if (rkDecide(rk_request_uri(a), main)) webkit_policy_decision_use(d);
		else webkit_policy_decision_ignore(d);
		return TRUE;
	}
	if (t == WEBKIT_POLICY_DECISION_TYPE_NEW_WINDOW_ACTION) {
		WebKitNavigationAction *a = webkit_navigation_policy_decision_get_navigation_action(WEBKIT_NAVIGATION_POLICY_DECISION(d));
		rkNewWindow(rk_request_uri(a));
		webkit_policy_decision_ignore(d);
		return TRUE;
	}
	return FALSE; // RESPONSE: để mặc định (tải về / hiển thị)
}

static GtkWidget *rk_create(WebKitWebView *v, WebKitNavigationAction *a, gpointer u) {
	rkNewWindow(rk_request_uri(a));
	return NULL; // không mở cửa sổ mới
}

static void rk_history(void) {
	rkHistory(webkit_web_view_can_go_back(WEBKIT_WEB_VIEW(rkContent)), webkit_web_view_can_go_forward(WEBKIT_WEB_VIEW(rkContent)));
}

static void rk_load_changed(WebKitWebView *v, WebKitLoadEvent e, gpointer u) {
	const char *uri = webkit_web_view_get_uri(v);
	rk_provisional = (e == WEBKIT_LOAD_STARTED || e == WEBKIT_LOAD_REDIRECTED);
	if (e == WEBKIT_LOAD_COMMITTED && uri) rkCommitted((char *)uri);
	if (e == WEBKIT_LOAD_FINISHED && uri) rkLoaded((char *)uri);
	if (e == WEBKIT_LOAD_COMMITTED || e == WEBKIT_LOAD_FINISHED) rk_history();
}

static void rk_title(GObject *o, GParamSpec *p, gpointer u) {
	const char *t = webkit_web_view_get_title(WEBKIT_WEB_VIEW(rkContent));
	rkTitle((char *)(t ? t : ""));
}

static void rk_toolbar_msg(WebKitUserContentManager *m, WebKitJavascriptResult *r, gpointer u) {
	JSCValue *v = webkit_javascript_result_get_js_value(r);
	char *s = jsc_value_to_string(v);
	rkToolbarMsg(s);
	g_free(s);
}

static gboolean rk_key(GtkWidget *w, GdkEventKey *e, gpointer u) {
	int ctrl = (e->state & GDK_CONTROL_MASK) != 0;
	int alt = (e->state & GDK_MOD1_MASK) != 0;
	int k = 0;
	switch (e->keyval) {
	case GDK_KEY_F5: k = 1; break;
	case GDK_KEY_F6: k = 2; break;
	case GDK_KEY_l: case GDK_KEY_L: if (ctrl) k = 2; break;
	case GDK_KEY_Left: if (alt) k = 3; break;
	case GDK_KEY_Right: if (alt) k = 4; break;
	}
	return k ? rkKey(k, ctrl, alt) : FALSE;
}

static gboolean rk_load_failed(WebKitWebView *v, WebKitLoadEvent e, gchar *uri, GError *err, gpointer u) {
	rk_provisional = 0;
	return FALSE; // để WebKit hiện trang lỗi mặc định
}

static gboolean rk_idle(gpointer u) { rkRunQueue(); return G_SOURCE_REMOVE; }
void rk_dispatch(void) { g_idle_add(rk_idle, NULL); }

void rk_create_window(const char *dataDir, const char *toolbarHTML, int toolbarHeight) {
	gtk_init(NULL, NULL);
	rkWin = gtk_window_new(GTK_WINDOW_TOPLEVEL);
	gtk_window_set_title(GTK_WINDOW(rkWin), "Trình duyệt — Rikkei LMS Connect");
	gtk_window_set_default_size(GTK_WINDOW(rkWin), 1200, 800);
	gtk_window_set_position(GTK_WINDOW(rkWin), GTK_WIN_POS_CENTER);
	g_signal_connect(rkWin, "destroy", G_CALLBACK(gtk_main_quit), NULL);
	g_signal_connect(rkWin, "key-press-event", G_CALLBACK(rk_key), NULL);

	GtkWidget *box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
	gtk_container_add(GTK_CONTAINER(rkWin), box);

	// Thanh công cụ: nhận tin nhắn qua window.webkit.messageHandlers.rk
	WebKitUserContentManager *ucm = webkit_user_content_manager_new();
	g_signal_connect(ucm, "script-message-received::rk", G_CALLBACK(rk_toolbar_msg), NULL);
	webkit_user_content_manager_register_script_message_handler(ucm, "rk");
	rkToolbar = webkit_web_view_new_with_user_content_manager(ucm);
	gtk_widget_set_size_request(rkToolbar, -1, toolbarHeight);
	WebKitSettings *ts = webkit_web_view_get_settings(WEBKIT_WEB_VIEW(rkToolbar));
	webkit_settings_set_enable_developer_extras(ts, FALSE);
	gtk_box_pack_start(GTK_BOX(box), rkToolbar, FALSE, FALSE, 0);

	// Trang web: hồ sơ (cookie, cache) riêng của trình duyệt
	WebKitWebsiteDataManager *dm = webkit_website_data_manager_new("base-data-directory", dataDir, "base-cache-directory", dataDir, NULL);
	WebKitWebContext *ctx = webkit_web_context_new_with_website_data_manager(dm);
	rkContent = webkit_web_view_new_with_context(ctx);
	WebKitSettings *cs = webkit_web_view_get_settings(WEBKIT_WEB_VIEW(rkContent));
	webkit_settings_set_enable_developer_extras(cs, FALSE);
	g_signal_connect(rkContent, "decide-policy", G_CALLBACK(rk_decide_policy), NULL);
	g_signal_connect(rkContent, "create", G_CALLBACK(rk_create), NULL);
	g_signal_connect(rkContent, "load-changed", G_CALLBACK(rk_load_changed), NULL);
	g_signal_connect(rkContent, "load-failed", G_CALLBACK(rk_load_failed), NULL);
	g_signal_connect(rkContent, "notify::title", G_CALLBACK(rk_title), NULL);
	gtk_box_pack_start(GTK_BOX(box), rkContent, TRUE, TRUE, 0);

	webkit_web_view_load_html(WEBKIT_WEB_VIEW(rkToolbar), toolbarHTML, NULL);
	gtk_widget_show_all(rkWin);
	gtk_window_present(GTK_WINDOW(rkWin));
	gtk_widget_grab_focus(rkContent);
}

void rk_run(void) { gtk_main(); }
void rk_navigate(const char *u) { rk_programmatic = 1; webkit_web_view_load_uri(WEBKIT_WEB_VIEW(rkContent), u); }
void rk_load_html(const char *h) { webkit_web_view_load_html(WEBKIT_WEB_VIEW(rkContent), h, NULL); }
void rk_back(void) { webkit_web_view_go_back(WEBKIT_WEB_VIEW(rkContent)); }
void rk_forward(void) { webkit_web_view_go_forward(WEBKIT_WEB_VIEW(rkContent)); }
void rk_reload(void) { webkit_web_view_reload(WEBKIT_WEB_VIEW(rkContent)); }
void rk_stop(void) { webkit_web_view_stop_loading(WEBKIT_WEB_VIEW(rkContent)); }
void rk_toolbar_eval(const char *js) {
#if WEBKIT_CHECK_VERSION(2, 40, 0)
	webkit_web_view_evaluate_javascript(WEBKIT_WEB_VIEW(rkToolbar), js, -1, NULL, NULL, NULL, NULL, NULL);
#else
	webkit_web_view_run_javascript(WEBKIT_WEB_VIEW(rkToolbar), js, NULL, NULL, NULL);
#endif
}
void rk_set_title(const char *t) { gtk_window_set_title(GTK_WINDOW(rkWin), t); }
void rk_focus(void) { gtk_window_present(GTK_WINDOW(rkWin)); }
void rk_focus_toolbar(void) { gtk_widget_grab_focus(rkToolbar); }
void rk_quit(void) { gtk_widget_destroy(rkWin); }
