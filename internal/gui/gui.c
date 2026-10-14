//go:build desktop && linux && cgo

#include "gui.h"
#include <dlfcn.h>
extern void goHolderAction(int action, char *value);
extern void goHolderDrain(void);
#include <string.h>
static GtkWidget *window, *status, *app_list, *header_bar, *detail_panel, *library_views, *empty_library;
static GtkCssProvider *app_css;
static GtkWidget *theme_selector;
static GtkWidget *default_auto_update, *default_sandbox, *default_sandbox_network;
static gchar *selected_theme;
static gboolean setting_theme;
static gboolean setting_app_defaults;
static gint sidebar_width = 190;
static GtkWidget *stack, *library_nav_buttons[2], *updates_nav_label;
static gint library_filter = 0;
static GtkWidget *summary, *detail_name, *detail_version, *detail_path, *detail_progress;
static GtkWidget *update_all_button, *pin_toggle, *sandbox_toggle, *sandbox_network_toggle;
static gboolean setting_pin = FALSE;
static gboolean setting_sandbox = FALSE;
static gboolean setting_auto_update = FALSE;
static GtkWidget *launch_button, *update_button, *rollback_button, *versions_button, *details_button, *remove_button, *uninstall_button, *search, *auto_update_toggle;
typedef struct {
 gchar *id, *name, *version, *path, *icon, *status, *fingerprint, *progress;
 gint percent, saved;
 gboolean active, can_update, can_rollback, pinned, auto_update, can_auto_update, sandboxed, sandbox_network;
 GtkWidget *row, *icon_widget, *name_label, *version_label, *state_label, *update_button, *launch_button;
} AppRow;
static void open_app_details(AppRow *app);
static AppRow *selected_app;
static GHashTable *rows_by_id;
static gchar *selected_id;
static gboolean rebuilding = FALSE;
static GHashTable *seen_rows;
static gboolean selected_dirty = FALSE;
static GObject *indicator;
static void *indicator_library;
static gboolean tray_connected = FALSE;
static gboolean start_in_tray = FALSE;
static gboolean user_opened_library = FALSE;
static GtkWidget *startup_toggle, *settings_locations, *settings_status, *settings_folders, *settings_remove_folder, *settings_version, *holder_update_button, *settings_keep_versions;
static gboolean setting_startup = FALSE;
static GtkWidget *label(const char *text, const char *style);
static GtkWidget *nav_button(const char *text) {
 GtkWidget *button = gtk_button_new_with_label(text);
 GtkWidget *child = gtk_bin_get_child(GTK_BIN(button));
 gtk_label_set_xalign(GTK_LABEL(child), 0.0);
 gtk_widget_set_hexpand(child, TRUE);
 gtk_button_set_relief(GTK_BUTTON(button), GTK_RELIEF_NONE);
 gtk_style_context_add_class(gtk_widget_get_style_context(button), "appholder-nav");
 return button;
}
static void show_settings(GtkButton *button, gpointer data) { gtk_stack_set_visible_child_name(GTK_STACK(stack), "settings"); gtk_header_bar_set_subtitle(GTK_HEADER_BAR(header_bar), "Preferences"); }
static void show_library_list(GtkButton *button, gpointer data) { gtk_stack_set_visible_child_name(GTK_STACK(library_views), "list"); gtk_header_bar_set_subtitle(GTK_HEADER_BAR(header_bar), library_filter ? "Updates" : "Applications"); }
static void select_library_view(GtkButton *button, gpointer data) {
 library_filter = GPOINTER_TO_INT(data);
 for (int i = 0; i < 2; i++) {
  GtkStyleContext *style = gtk_widget_get_style_context(library_nav_buttons[i]);
  if (i == library_filter) gtk_style_context_add_class(style, "appholder-nav-selected");
  else gtk_style_context_remove_class(style, "appholder-nav-selected");
 }
 gtk_stack_set_visible_child_name(GTK_STACK(stack), "library");
 gtk_stack_set_visible_child_name(GTK_STACK(library_views), "list");
 gtk_header_bar_set_subtitle(GTK_HEADER_BAR(header_bar), library_filter ? "Updates" : "Applications");
 if (app_list) gtk_list_box_invalidate_filter(GTK_LIST_BOX(app_list));
}
static void startup_changed(GtkToggleButton *button, gpointer data) { if (!setting_startup) goHolderAction(9, gtk_toggle_button_get_active(button) ? "enable" : "disable"); }
static void sandbox_changed(GtkToggleButton *button, gpointer data) { if (!setting_sandbox && selected_id) { gchar *request=g_strdup_printf("%s|%d|%d",selected_id,gtk_toggle_button_get_active(GTK_TOGGLE_BUTTON(sandbox_toggle)),gtk_toggle_button_get_active(GTK_TOGGLE_BUTTON(sandbox_network_toggle)));goHolderAction(23,request);g_free(request); } }
static void auto_update_changed(GtkToggleButton *button, gpointer data) { if (!setting_auto_update && selected_app && selected_app->can_auto_update) goHolderAction(gtk_toggle_button_get_active(button) ? 20 : 21, selected_app->id); }
void holder_app_defaults(int automatic,int sandbox,int network){setting_app_defaults=TRUE;gtk_toggle_button_set_active(GTK_TOGGLE_BUTTON(default_auto_update),automatic);gtk_toggle_button_set_active(GTK_TOGGLE_BUTTON(default_sandbox),sandbox);gtk_toggle_button_set_active(GTK_TOGGLE_BUTTON(default_sandbox_network),network);setting_app_defaults=FALSE;}
static void save_app_defaults(GtkToggleButton *button,gpointer data){if(setting_app_defaults)return;gchar *value=g_strdup_printf("%d|%d|%d",gtk_toggle_button_get_active(GTK_TOGGLE_BUTTON(default_auto_update)),gtk_toggle_button_get_active(GTK_TOGGLE_BUTTON(default_sandbox)),gtk_toggle_button_get_active(GTK_TOGGLE_BUTTON(default_sandbox_network)));goHolderAction(25,value);g_free(value);}
void holder_startup_state(int enabled) { setting_startup = TRUE; gtk_toggle_button_set_active(GTK_TOGGLE_BUTTON(startup_toggle), enabled); setting_startup = FALSE; }
static void show_library(GtkMenuItem *item, gpointer data) {
 user_opened_library = TRUE;
 select_library_view(NULL, GINT_TO_POINTER(0));
 gtk_widget_show_all(window); gtk_window_present(GTK_WINDOW(window));
 goHolderAction(0, "");
}
static void quit_desktop(GtkMenuItem *item, gpointer data) { gtk_main_quit(); }
void holder_present(void) { show_library(NULL, NULL); }
static gboolean close_library(GtkWidget *widget, GdkEvent *event, gpointer data) {
 if (tray_connected) { gtk_widget_hide(window); return TRUE; }
 return FALSE;
}
static void connection_changed(GObject *object, gboolean connected, gpointer data) {
 tray_connected = connected;
 if (connected && start_in_tray && !user_opened_library) gtk_widget_hide(window);
 // Never leave an inaccessible hidden window after the tray host disappears.
 if (!connected) gtk_widget_show_all(window);
}


static void check_updates(GtkButton *button, gpointer data) { goHolderAction(5, ""); }
static void refresh(GtkButton *button, gpointer data) { goHolderAction(0, ""); }
static void launch(GtkButton *button, gpointer data) {
 if (selected_app) {
  goHolderAction(1, selected_app->id);
 } else holder_status("Select an application first.");
}
static void update_all(GtkButton *button, gpointer data) {
 GtkWidget *dialog = gtk_message_dialog_new(GTK_WINDOW(window), GTK_DIALOG_MODAL, GTK_MESSAGE_QUESTION, GTK_BUTTONS_OK_CANCEL, "Install all available updates?");
 gtk_message_dialog_format_secondary_text(GTK_MESSAGE_DIALOG(dialog), "Versions marked Keep this version are excluded. Each updated file is backed up.");
 int answer = gtk_dialog_run(GTK_DIALOG(dialog)); gtk_widget_destroy(dialog); if (answer == GTK_RESPONSE_OK) goHolderAction(12, "");
}
static void pin_changed(GtkToggleButton *button, gpointer data) { if (!setting_pin && selected_id) goHolderAction(gtk_toggle_button_get_active(button) ? 13 : 14, selected_id); }
static void selected_action(GtkButton *button, gpointer data) {
 int action = GPOINTER_TO_INT(data);
 if (!selected_app) { holder_status("Select an application first."); return; }
 gchar *id = g_strdup(selected_app->id);
 if (action == 8 || action == 11) { goHolderAction(action, id); g_free(id); return; }
 const char *question = action == 6 ? "Install the available update?" : action == 15 ? "Remove this application from AppHolder?" : action == 19 ? "Uninstall this AppImage?" : "Restore the most recently saved version?";
 GtkWidget *dialog = gtk_message_dialog_new(GTK_WINDOW(window), GTK_DIALOG_MODAL, GTK_MESSAGE_QUESTION, GTK_BUTTONS_OK_CANCEL, "%s", question);
 const char *detail = action == 15 ? "The AppImage file and saved versions are kept. AppHolder will ignore this file until you integrate it again." : action == 19 ? "The selected AppImage file will be permanently deleted. Saved backups are kept. Other installed copies are not removed." : "The current file will be saved for rollback. Running applications are not restarted.";
 gtk_message_dialog_format_secondary_text(GTK_MESSAGE_DIALOG(dialog), "%s", detail);
 int answer = gtk_dialog_run(GTK_DIALOG(dialog)); gtk_widget_destroy(dialog);
 if (answer == GTK_RESPONSE_OK) goHolderAction(action, id);
 g_free(id);
}
void holder_versions(char *id, char *text) {
 GtkWidget *dialog = gtk_dialog_new_with_buttons("Saved versions", GTK_WINDOW(window), GTK_DIALOG_MODAL | GTK_DIALOG_DESTROY_WITH_PARENT, "Cancel", GTK_RESPONSE_CANCEL, "Restore selected", GTK_RESPONSE_ACCEPT, NULL);
 gtk_window_set_default_size(GTK_WINDOW(dialog), 440, 180);
 GtkWidget *area=gtk_dialog_get_content_area(GTK_DIALOG(dialog));
 GtkWidget *label=gtk_label_new("Choose the saved version to restore. The current version will be saved first."); gtk_label_set_line_wrap(GTK_LABEL(label),TRUE); gtk_box_pack_start(GTK_BOX(area),label,FALSE,FALSE,12);
 GtkWidget *combo=gtk_combo_box_text_new(); gchar **lines=g_strsplit(text,"\n",-1); int count=0; for(;lines[count];count++) gtk_combo_box_text_append_text(GTK_COMBO_BOX_TEXT(combo),lines[count]); if(count>0) gtk_combo_box_set_active(GTK_COMBO_BOX(combo),0); g_strfreev(lines); gtk_box_pack_start(GTK_BOX(area),combo,FALSE,FALSE,8);
 gtk_widget_show_all(area);
 if(gtk_dialog_run(GTK_DIALOG(dialog))==GTK_RESPONSE_ACCEPT){int selected=gtk_combo_box_get_active(GTK_COMBO_BOX(combo)); if(selected>=0 && selected<count){char request[128]; snprintf(request,sizeof(request),"%s:%d",id,count-selected-1); goHolderAction(22,request);}}
 gtk_widget_destroy(dialog);
}
static void choose(GtkButton *button, gpointer data) {
 GtkWidget *dialog = gtk_file_chooser_dialog_new("Open an AppImage", GTK_WINDOW(window), GTK_FILE_CHOOSER_ACTION_OPEN, "Cancel", GTK_RESPONSE_CANCEL, "Open", GTK_RESPONSE_ACCEPT, NULL);
 GtkFileFilter *filter = gtk_file_filter_new(); gtk_file_filter_set_name(filter, "AppImages"); gtk_file_filter_add_pattern(filter, "*.AppImage"); gtk_file_filter_add_pattern(filter, "*.appimage"); gtk_file_chooser_add_filter(GTK_FILE_CHOOSER(dialog), filter);
 if (gtk_dialog_run(GTK_DIALOG(dialog)) == GTK_RESPONSE_ACCEPT) {
  gchar *path = gtk_file_chooser_get_filename(GTK_FILE_CHOOSER(dialog));
  gtk_widget_destroy(dialog);
  holder_open(path);
  g_free(path);
 } else gtk_widget_destroy(dialog);
}
static gchar *dropped_local_path(GtkSelectionData *selection_data) {
 gchar **uris = gtk_selection_data_get_uris(selection_data);
 if (!uris || !uris[0]) { g_strfreev(uris); return NULL; }
 gchar *path = g_filename_from_uri(uris[0], NULL, NULL);
 g_strfreev(uris);
 if (!path) return NULL;
 gchar *lower = g_ascii_strdown(path, -1);
 gboolean appimage = g_str_has_suffix(lower, ".appimage");
 g_free(lower);
 if (!appimage) { g_free(path); return NULL; }
 return path;
}
typedef struct { gchar **path; GtkWidget *label; } ChoiceDrop;
static void receive_drop(GtkWidget *widget, GdkDragContext *context, gint x, gint y, GtkSelectionData *data, guint info, guint time, gpointer user_data) {
 gchar *path = dropped_local_path(data);
 if (path) {
  gtk_drag_finish(context, TRUE, FALSE, time);
  holder_open(path);
  g_free(path);
 } else {
  gtk_drag_finish(context, FALSE, FALSE, time);
 }
}
static void receive_choice_drop(GtkWidget *widget, GdkDragContext *context, gint x, gint y, GtkSelectionData *data, guint info, guint time, gpointer user_data) {
 ChoiceDrop *drop = user_data;
 gchar *path = dropped_local_path(data);
 if (path) {
  g_free(*drop->path);
  *drop->path = path;
  gtk_label_set_text(GTK_LABEL(drop->label), path);
  gtk_drag_finish(context, TRUE, FALSE, time);
 } else {
  gtk_drag_finish(context, FALSE, FALSE, time);
 }
}
static void setup_tray(void) {
 const char *libraries[] = {"libayatana-appindicator3.so.1", "libappindicator3.so.1"};
 for (int i = 0; i < 2 && !indicator_library; i++) indicator_library = dlopen(libraries[i], RTLD_NOW | RTLD_LOCAL);
 if (!indicator_library) return;
 typedef GObject *(*NewIndicator)(const gchar *, const gchar *, int);
 typedef void (*SetMenu)(GObject *, GtkMenu *);
 typedef void (*SetStatus)(GObject *, int);
 NewIndicator create = (NewIndicator)dlsym(indicator_library, "app_indicator_new");
 SetMenu set_menu = (SetMenu)dlsym(indicator_library, "app_indicator_set_menu");
 SetStatus set_status = (SetStatus)dlsym(indicator_library, "app_indicator_set_status");
 if (!create || !set_menu || !set_status) { dlclose(indicator_library); indicator_library = NULL; return; }
 indicator = create("appholder", "appholder-symbolic", 0);
 if (!indicator) return;
 GtkWidget *menu = gtk_menu_new();
 const char *labels[] = {"Open library", "Open AppImage…", "Check for updates", "Refresh library", "Quit tray and library"};
 GCallback callbacks[] = {G_CALLBACK(show_library), G_CALLBACK(choose), G_CALLBACK(check_updates), G_CALLBACK(refresh), G_CALLBACK(quit_desktop)};
 for (int i = 0; i < 5; i++) {
  GtkWidget *item = gtk_menu_item_new_with_label(labels[i]);
  g_signal_connect(item, "activate", callbacks[i], NULL);
  gtk_menu_shell_append(GTK_MENU_SHELL(menu), item);
 }
 gtk_widget_show_all(menu);
 g_signal_connect(indicator, "connection-changed", G_CALLBACK(connection_changed), NULL);
 set_menu(indicator, GTK_MENU(menu)); set_status(indicator, 1);
}
void holder_tray_mode(void) { start_in_tray = TRUE; }
void holder_open(char *path) {
  GtkWidget *dialog = gtk_dialog_new_with_buttons("Open AppImage", GTK_WINDOW(window), GTK_DIALOG_MODAL | GTK_DIALOG_DESTROY_WITH_PARENT, "Cancel", GTK_RESPONSE_CANCEL, NULL);
  gtk_window_set_default_size(GTK_WINDOW(dialog), 560, 300);
  gtk_window_set_resizable(GTK_WINDOW(dialog), TRUE);
  GtkWidget *area = gtk_dialog_get_content_area(GTK_DIALOG(dialog));
  gtk_container_set_border_width(GTK_CONTAINER(area), 24);
  GtkWidget *content = gtk_box_new(GTK_ORIENTATION_VERTICAL, 16);
  gtk_box_pack_start(GTK_BOX(area), content, TRUE, TRUE, 0);
  GtkWidget *title = label("What would you like to do?", "appholder-title");
  GtkWidget *description = label("Run it without adding it to your library, or integrate it so it appears in AppHolder and your applications menu.", "appholder-subtitle");
  gtk_label_set_line_wrap(GTK_LABEL(description), TRUE);
  gtk_box_pack_start(GTK_BOX(content), title, FALSE, FALSE, 0);
  gtk_box_pack_start(GTK_BOX(content), description, FALSE, FALSE, 0);
  gchar *selected_path = g_strdup(path);
  GtkWidget *path_label = gtk_label_new(selected_path);
  gtk_widget_set_halign(path_label, GTK_ALIGN_FILL);
  gtk_label_set_xalign(GTK_LABEL(path_label), 0.0);
  gtk_label_set_ellipsize(GTK_LABEL(path_label), PANGO_ELLIPSIZE_MIDDLE);
  gtk_label_set_selectable(GTK_LABEL(path_label), TRUE);
  gtk_box_pack_start(GTK_BOX(content), path_label, FALSE, FALSE, 0);
  GtkWidget *drop_hint = label("Drop an AppImage here to choose a different file", "appholder-subtitle");
  gtk_box_pack_start(GTK_BOX(content), drop_hint, TRUE, TRUE, 0);
  ChoiceDrop drop = {&selected_path, path_label};
  GtkTargetEntry target = {"text/uri-list", 0, 0};
 gtk_drag_dest_set(dialog, GTK_DEST_DEFAULT_ALL, &target, 1, GDK_ACTION_COPY);
  g_signal_connect(dialog, "drag-data-received", G_CALLBACK(receive_choice_drop), &drop);
  gtk_dialog_add_button(GTK_DIALOG(dialog), "Run once", 1);
  gtk_dialog_add_button(GTK_DIALOG(dialog), "Integrate", 2);
  gtk_dialog_add_button(GTK_DIALOG(dialog), "Integrate and run", 3);
  gtk_widget_show_all(area);
  gint answer = gtk_dialog_run(GTK_DIALOG(dialog));
  if (answer >= 1 && answer <= 3) goHolderAction(answer + 1, selected_path);
  g_free(selected_path);
  gtk_widget_destroy(dialog);
}
static GtkWidget *label(const char *text, const char *style) {
 GtkWidget *widget = gtk_label_new(text); gtk_widget_set_halign(widget, GTK_ALIGN_START);
 if (style) gtk_style_context_add_class(gtk_widget_get_style_context(widget), style);
 return widget;
}
static void free_app_row(gpointer data) {
 AppRow *app = data;
 g_free(app->id); g_free(app->name); g_free(app->version); g_free(app->path); g_free(app->icon); g_free(app->status); g_free(app->fingerprint); g_free(app->progress); g_free(app);
}
static void set_string(gchar **target, const gchar *value) { g_free(*target); *target = g_strdup(value ? value : ""); }
static gboolean filter_app_row(GtkListBoxRow *row, gpointer data) {
 AppRow *app = g_object_get_data(G_OBJECT(row), "appholder-app");
 if (library_filter == 1 && !app->can_update) return FALSE;
 const gchar *query = gtk_entry_get_text(GTK_ENTRY(search)); if (!query[0]) return TRUE;
 gchar *name = g_utf8_casefold(app->name, -1), *needle = g_utf8_casefold(query, -1), *path = g_utf8_casefold(app->path, -1);
 gboolean found = strstr(name, needle) != NULL || strstr(path, needle) != NULL;
 g_free(name); g_free(needle); g_free(path); return found;
}
static gint sort_app_rows(GtkListBoxRow *left, GtkListBoxRow *right, gpointer data) {
 AppRow *a = g_object_get_data(G_OBJECT(left), "appholder-app"), *b = g_object_get_data(G_OBJECT(right), "appholder-app");
 return g_utf8_collate(a->name, b->name);
}
static void app_row_action(GtkButton *button, gpointer data) {
 GtkWidget *row = data; AppRow *app = g_object_get_data(G_OBJECT(row), "appholder-app");
 if (!app) return;
 int action=GPOINTER_TO_INT(g_object_get_data(G_OBJECT(button), "appholder-action"));
 if(action==99){open_app_details(app);return;}
 goHolderAction(action, app->id);
}
static GtkWidget *row_button(const gchar *text, gint action, GtkWidget *row) {
 GtkWidget *button = gtk_button_new_with_label(text);
 g_object_set_data(G_OBJECT(button), "appholder-action", GINT_TO_POINTER(action));
 g_signal_connect(button, "clicked", G_CALLBACK(app_row_action), row);
 return button;
}
static GtkWidget *make_app_row(AppRow *app) {
 GtkWidget *row = gtk_list_box_row_new(); app->row = row;
 GtkWidget *layout = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 16);
 gtk_container_set_border_width(GTK_CONTAINER(layout), 16);
 gtk_container_add(GTK_CONTAINER(row), layout);
 app->icon_widget = gtk_image_new_from_icon_name("application-x-executable", GTK_ICON_SIZE_DIALOG);
 gtk_widget_set_size_request(app->icon_widget, 44, 44);
 gtk_box_pack_start(GTK_BOX(layout), app->icon_widget, FALSE, FALSE, 0);
 GtkWidget *identity = gtk_box_new(GTK_ORIENTATION_VERTICAL, 4);
 gtk_widget_set_hexpand(identity, TRUE);
 app->name_label = gtk_label_new(""); gtk_widget_set_halign(app->name_label, GTK_ALIGN_START); gtk_label_set_single_line_mode(GTK_LABEL(app->name_label),TRUE); gtk_label_set_ellipsize(GTK_LABEL(app->name_label),PANGO_ELLIPSIZE_END); gtk_label_set_max_width_chars(GTK_LABEL(app->name_label),34); gtk_style_context_add_class(gtk_widget_get_style_context(app->name_label), "app-card-name");
 app->version_label = gtk_label_new(""); gtk_widget_set_halign(app->version_label, GTK_ALIGN_START); gtk_label_set_single_line_mode(GTK_LABEL(app->version_label),TRUE); gtk_label_set_ellipsize(GTK_LABEL(app->version_label),PANGO_ELLIPSIZE_END); gtk_label_set_max_width_chars(GTK_LABEL(app->version_label),38); gtk_style_context_add_class(gtk_widget_get_style_context(app->version_label), "appholder-subtitle");
 gtk_box_pack_start(GTK_BOX(identity), app->name_label, FALSE, FALSE, 0); gtk_box_pack_start(GTK_BOX(identity), app->version_label, FALSE, FALSE, 0);
 gtk_box_pack_start(GTK_BOX(layout), identity, TRUE, TRUE, 0);
 app->state_label = gtk_label_new(""); gtk_widget_set_size_request(app->state_label, 110, -1); gtk_label_set_ellipsize(GTK_LABEL(app->state_label), PANGO_ELLIPSIZE_END); gtk_box_pack_start(GTK_BOX(layout), app->state_label, FALSE, FALSE, 0);
 app->update_button = row_button("Update", 6, row); gtk_style_context_add_class(gtk_widget_get_style_context(app->update_button), "suggested-action"); gtk_box_pack_start(GTK_BOX(layout), app->update_button, FALSE, FALSE, 0);
 app->launch_button = row_button("Launch", 1, row); gtk_box_pack_start(GTK_BOX(layout), app->launch_button, FALSE, FALSE, 0);
 GtkWidget *options_button = row_button("Options", 99, row); gtk_box_pack_start(GTK_BOX(layout), options_button, FALSE, FALSE, 0);
 g_object_set_data_full(G_OBJECT(row), "appholder-app", app, free_app_row);
 return row;
}
static void update_app_row(AppRow *app) {
 gtk_label_set_text(GTK_LABEL(app->name_label), app->name);
 gchar *version = app->version[0] ? g_strdup_printf("Version %s", app->version) : g_strdup("Version unknown");
 gtk_label_set_text(GTK_LABEL(app->version_label), version); g_free(version);
 gtk_label_set_text(GTK_LABEL(app->state_label), app->status);
 gtk_widget_set_tooltip_text(app->state_label, app->status);
 GtkStyleContext *state_style = gtk_widget_get_style_context(app->state_label);
 gtk_style_context_remove_class(state_style, "app-card-state"); gtk_style_context_remove_class(state_style, "app-card-update");
 if (app->can_update) gtk_style_context_add_class(state_style, "app-card-update");
 else if (!g_strcmp0(app->status, "Up to date")) gtk_style_context_add_class(state_style, "app-card-state");
 gtk_widget_set_visible(app->update_button, app->can_update);
 gtk_widget_set_sensitive(app->update_button, !app->active);
 gtk_widget_set_sensitive(app->launch_button, TRUE);
 GdkPixbuf *pixbuf = NULL;
 if (app->icon && app->icon[0]) pixbuf = gdk_pixbuf_new_from_file_at_scale(app->icon, 44, 44, TRUE, NULL);
 if (pixbuf) { gtk_image_set_from_pixbuf(GTK_IMAGE(app->icon_widget), pixbuf); g_object_unref(pixbuf); }
 else gtk_image_set_from_icon_name(GTK_IMAGE(app->icon_widget), "application-x-executable", GTK_ICON_SIZE_DIALOG);
}
static void render_selected_app(AppRow *app) {
 selected_app = app;
 g_free(selected_id); selected_id = app ? g_strdup(app->id) : NULL;
 gboolean has = app != NULL;
 gtk_widget_set_sensitive(pin_toggle, has); gtk_widget_set_sensitive(sandbox_toggle, has);
 gtk_widget_set_sensitive(auto_update_toggle, has && app->can_auto_update);
 gtk_widget_set_sensitive(details_button, has); gtk_widget_set_sensitive(remove_button, has && !app->active); gtk_widget_set_sensitive(uninstall_button, has && !app->active);
 gtk_widget_set_sensitive(launch_button, has); gtk_widget_set_sensitive(update_button, has && app->can_update && !app->active);
 gtk_widget_set_sensitive(rollback_button, has && app->can_rollback && !app->active); gtk_widget_set_sensitive(versions_button, has && app->saved > 0);
 if (!has) { gtk_label_set_text(GTK_LABEL(detail_name), "Select an application"); gtk_label_set_text(GTK_LABEL(detail_version), "Launch, update, or restore a saved version."); gtk_label_set_text(GTK_LABEL(detail_path), ""); gtk_widget_hide(detail_progress); return; }
 setting_pin = TRUE; gtk_toggle_button_set_active(GTK_TOGGLE_BUTTON(pin_toggle), app->pinned); setting_pin = FALSE;
 setting_auto_update = TRUE; gtk_toggle_button_set_active(GTK_TOGGLE_BUTTON(auto_update_toggle), app->auto_update); setting_auto_update = FALSE;
 setting_sandbox = TRUE; gtk_toggle_button_set_active(GTK_TOGGLE_BUTTON(sandbox_toggle), app->sandboxed); gtk_toggle_button_set_active(GTK_TOGGLE_BUTTON(sandbox_network_toggle), app->sandbox_network); gtk_widget_set_sensitive(sandbox_network_toggle, app->sandboxed); setting_sandbox = FALSE;
 gtk_label_set_text(GTK_LABEL(detail_name), app->name);
 gchar *description = g_strdup_printf("Version %s · %s", app->version[0] ? app->version : "Unknown", app->status); gtk_label_set_text(GTK_LABEL(detail_version), description); g_free(description);
 gtk_label_set_text(GTK_LABEL(detail_path), app->path); gtk_widget_set_tooltip_text(detail_path, app->path);
 if (app->active) { gtk_widget_show(detail_progress); gtk_progress_bar_set_text(GTK_PROGRESS_BAR(detail_progress), app->progress); if (app->percent >= 0) gtk_progress_bar_set_fraction(GTK_PROGRESS_BAR(detail_progress), app->percent / 100.0); else gtk_progress_bar_pulse(GTK_PROGRESS_BAR(detail_progress)); } else gtk_widget_hide(detail_progress);
}
static void app_selection_changed(GtkListBox *box, GtkListBoxRow *row, gpointer data) {
 if (rebuilding) return;
 AppRow *app=row?g_object_get_data(G_OBJECT(row),"appholder-app"):NULL;
 if(app) open_app_details(app); else render_selected_app(NULL);
}
static void open_app_details(AppRow *app) { if(!app)return; render_selected_app(app); gtk_stack_set_visible_child_name(GTK_STACK(library_views),"details"); gtk_header_bar_set_subtitle(GTK_HEADER_BAR(header_bar),app->name); }
static void apply_theme(void) {
 GtkSettings *settings=gtk_settings_get_default(); gboolean dark=FALSE;
 if (!g_strcmp0(selected_theme,"dark")) dark=TRUE;
 else if (!g_strcmp0(selected_theme,"system")) g_object_get(settings,"gtk-application-prefer-dark-theme",&dark,NULL);
 const gchar *dark_css="window{background-color:#1E2227;color:#F4F2EE}.appholder-title,.app-card-name{color:#F4F2EE;font-weight:bold}.appholder-subtitle{color:#8C939B;opacity:1}.appholder-detail{padding:18px}.app-card{margin:6px 4px;background-color:#292E35;color:#F4F2EE;border:1px solid #4A5260}.appholder-sidebar{background-color:#1E2227;padding:16px 10px}.appholder-section{color:#8C939B;font-weight:bold;padding:12px 10px 4px}.appholder-nav{color:#F4F2EE;padding:8px 12px}.appholder-nav-selected,.app-card:selected{background-color:alpha(#3A6EA5,0.3);color:#F4F2EE;border-color:#3A6EA5}.app-card-state{color:#8C939B}.app-card-update{color:#C8CCD1}button{color:#F4F2EE;background-image:none;background-color:#292E35;border:1px solid #4A5260;padding:7px 12px}button.suggested-action{background-image:none;background-color:#3A6EA5;color:#F4F2EE;border-color:#3A6EA5}";
 const gchar *light_css="window{background-color:#F4F2EE;color:#1E2227}.appholder-title,.app-card-name{color:#1E2227;font-weight:bold}.appholder-subtitle{color:#8C939B;opacity:1}.appholder-detail{padding:18px}.app-card{margin:6px 4px;background-color:#FFFFFF;color:#1E2227;border:1px solid #C8CCD1}.appholder-sidebar{background-color:#E9EBED;padding:16px 10px}.appholder-section{color:#7C848E;font-weight:bold;padding:12px 10px 4px}.appholder-nav{color:#1E2227;padding:8px 12px}.appholder-nav-selected,.app-card:selected{background-color:alpha(#3A6EA5,0.16);color:#1E2227;border-color:#3A6EA5}.app-card-state{color:#7C848E}.app-card-update{color:#3A6EA5}button{color:#1E2227;background-image:none;background-color:#FFFFFF;border:1px solid #C8CCD1;padding:7px 12px}button.suggested-action{background-image:none;background-color:#3A6EA5;color:#F4F2EE;border-color:#3A6EA5}";
 gtk_css_provider_load_from_data(app_css,dark?dark_css:light_css,-1,NULL);
}
void holder_theme(char *theme) { g_free(selected_theme); selected_theme=g_strdup(theme); setting_theme=TRUE; if(theme_selector) gtk_combo_box_set_active_id(GTK_COMBO_BOX(theme_selector),selected_theme); setting_theme=FALSE; apply_theme(); }
static void theme_changed(GtkComboBox *combo,gpointer data) { if(!setting_theme){const gchar *theme=gtk_combo_box_get_active_id(combo);if(theme)goHolderAction(24,(gchar*)theme);} }
static void system_theme_changed(GObject *object,GParamSpec *spec,gpointer data){if(!g_strcmp0(selected_theme,"system"))apply_theme();}
static void window_resized(GtkWidget *widget,GtkAllocation *allocation,gpointer data){
 GtkWidget *sidebar=data;
 gint width=CLAMP((gint)(allocation->width*0.14),168,224);
 if(width!=sidebar_width){sidebar_width=width;gtk_widget_set_size_request(sidebar,width,-1);}
}
static void search_changed(GtkSearchEntry *entry, gpointer data) { gtk_list_box_invalidate_filter(GTK_LIST_BOX(app_list)); }
static gboolean poll_library(gpointer data) { goHolderAction(0, ""); return G_SOURCE_CONTINUE; }
static gboolean drain_events(gpointer data) { goHolderDrain(); return G_SOURCE_REMOVE; }
void holder_wake(void) { g_idle_add(drain_events, NULL); }
static GtkWidget *action_button(const char *text, int action) {
 GtkWidget *button = gtk_button_new_with_label(text); g_signal_connect(button, "clicked", G_CALLBACK(selected_action), GINT_TO_POINTER(action)); return button;
}
static void folder_selection_changed(GtkListBox *box, GtkListBoxRow *row, gpointer data) {
 GList *rows = gtk_container_get_children(GTK_CONTAINER(box));
 gtk_widget_set_sensitive(settings_remove_folder, row != NULL && g_list_length(rows) > 1);
 g_list_free(rows);
}
static gchar *folder_list_text(void) {
 GString *text = g_string_new(NULL);
 GList *rows = gtk_container_get_children(GTK_CONTAINER(settings_folders));
 for (GList *item = rows; item; item = item->next) {
  GtkWidget *child = gtk_bin_get_child(GTK_BIN(item->data));
  const gchar *path = gtk_label_get_text(GTK_LABEL(child));
  if (text->len) g_string_append_c(text, '\n');
  g_string_append(text, path);
 }
 g_list_free(rows);
 return g_string_free(text, FALSE);
}
static void append_folder_row(const gchar *path) {
 GtkWidget *row = gtk_label_new(path);
 gtk_widget_set_halign(row, GTK_ALIGN_START);
 gtk_label_set_ellipsize(GTK_LABEL(row), PANGO_ELLIPSIZE_MIDDLE);
 gtk_widget_set_margin_start(row, 10); gtk_widget_set_margin_end(row, 10); gtk_widget_set_margin_top(row, 8); gtk_widget_set_margin_bottom(row, 8);
 gtk_list_box_insert(GTK_LIST_BOX(settings_folders), row, -1);
 gtk_widget_show_all(gtk_widget_get_parent(row));
}
static void set_folder_list(const gchar *folders) {
 GList *rows = gtk_container_get_children(GTK_CONTAINER(settings_folders));
 for (GList *item = rows; item; item = item->next) gtk_widget_destroy(GTK_WIDGET(item->data));
 g_list_free(rows);
 gchar **paths = g_strsplit(folders, "\n", -1);
 for (int i = 0; paths[i]; i++) if (paths[i][0]) append_folder_row(paths[i]);
 g_strfreev(paths);
 folder_selection_changed(GTK_LIST_BOX(settings_folders), gtk_list_box_get_selected_row(GTK_LIST_BOX(settings_folders)), NULL);
}
static void save_folders(GtkButton *button, gpointer data) {
 gchar *text=folder_list_text();gchar *request=g_strdup_printf("%s\n--KEEP--\n%d",text,gtk_spin_button_get_value_as_int(GTK_SPIN_BUTTON(settings_keep_versions)));goHolderAction(16,request);g_free(request);g_free(text);
}
static void add_folder(GtkButton *button, gpointer data) {
 GtkWidget *dialog=gtk_file_chooser_dialog_new("Add watched folder",GTK_WINDOW(window),GTK_FILE_CHOOSER_ACTION_SELECT_FOLDER,"Cancel",GTK_RESPONSE_CANCEL,"Add",GTK_RESPONSE_ACCEPT,NULL);
 if(gtk_dialog_run(GTK_DIALOG(dialog))==GTK_RESPONSE_ACCEPT){gchar *path=gtk_file_chooser_get_filename(GTK_FILE_CHOOSER(dialog));gchar *existing=folder_list_text();gboolean duplicate=FALSE;gchar **paths=g_strsplit(existing,"\n",-1);for(int i=0;paths[i];i++)if(!g_strcmp0(paths[i],path))duplicate=TRUE;if(!duplicate)append_folder_row(path);g_strfreev(paths);g_free(existing);g_free(path);}gtk_widget_destroy(dialog);
}
static void remove_folder(GtkButton *button, gpointer data) {
 GtkListBoxRow *row=gtk_list_box_get_selected_row(GTK_LIST_BOX(settings_folders));
 if(row && gtk_widget_get_sensitive(settings_remove_folder)) { gtk_widget_destroy(GTK_WIDGET(row)); folder_selection_changed(GTK_LIST_BOX(settings_folders),gtk_list_box_get_selected_row(GTK_LIST_BOX(settings_folders)),NULL); }
}
static void holder_check_update(GtkButton *button,gpointer data){goHolderAction(17,"");}
static void holder_install_update(GtkButton *button,gpointer data){GtkWidget *dialog=gtk_message_dialog_new(GTK_WINDOW(window),GTK_DIALOG_MODAL,GTK_MESSAGE_QUESTION,GTK_BUTTONS_OK_CANCEL,"Update AppHolder?");gtk_message_dialog_format_secondary_text(GTK_MESSAGE_DIALOG(dialog),"Administrator authorization may be required. Restart AppHolder after installation.");int answer=gtk_dialog_run(GTK_DIALOG(dialog));gtk_widget_destroy(dialog);if(answer==GTK_RESPONSE_OK)goHolderAction(18,"");}
int holder_init(void) {
 if (!gtk_init_check(NULL, NULL)) return 0;
 app_css = gtk_css_provider_new();
 gtk_style_context_add_provider_for_screen(gdk_screen_get_default(), GTK_STYLE_PROVIDER(app_css), GTK_STYLE_PROVIDER_PRIORITY_APPLICATION);
 selected_theme=g_strdup("system"); apply_theme(); g_signal_connect(gtk_settings_get_default(),"notify::gtk-application-prefer-dark-theme",G_CALLBACK(system_theme_changed),NULL);
 window = gtk_window_new(GTK_WINDOW_TOPLEVEL); gtk_window_set_title(GTK_WINDOW(window), "AppHolder"); gtk_window_set_icon_name(GTK_WINDOW(window), "appholder"); gtk_window_set_default_size(GTK_WINDOW(window), 1100, 720);
 g_signal_connect(window, "delete-event", G_CALLBACK(close_library), NULL); g_signal_connect(window, "destroy", G_CALLBACK(gtk_main_quit), NULL);
 header_bar = gtk_header_bar_new(); gtk_header_bar_set_title(GTK_HEADER_BAR(header_bar), "AppHolder"); gtk_header_bar_set_subtitle(GTK_HEADER_BAR(header_bar), "Applications"); gtk_header_bar_set_show_close_button(GTK_HEADER_BAR(header_bar), TRUE); gtk_window_set_titlebar(GTK_WINDOW(window), header_bar);
 GtkWidget *body = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 0); gtk_container_add(GTK_CONTAINER(window), body);
 stack = gtk_stack_new(); gtk_stack_set_transition_type(GTK_STACK(stack), GTK_STACK_TRANSITION_TYPE_CROSSFADE);
 GtkWidget *sidebar = gtk_box_new(GTK_ORIENTATION_VERTICAL, 4); gtk_widget_set_size_request(sidebar, sidebar_width, -1); gtk_widget_set_vexpand(sidebar, TRUE); gtk_widget_set_valign(sidebar, GTK_ALIGN_FILL); gtk_style_context_add_class(gtk_widget_get_style_context(sidebar), "appholder-sidebar"); gtk_box_pack_start(GTK_BOX(body), sidebar, FALSE, TRUE, 0); gtk_box_pack_start(GTK_BOX(body), stack, TRUE, TRUE, 0); g_signal_connect(window,"size-allocate",G_CALLBACK(window_resized),sidebar);
 gtk_box_pack_start(GTK_BOX(sidebar), label("Library", "appholder-section"), FALSE, FALSE, 0);
 const char *nav_labels[] = {"◈   Applications", "↻   Updates"};
 for (int i = 0; i < 2; i++) { library_nav_buttons[i] = nav_button(nav_labels[i]); g_signal_connect(library_nav_buttons[i], "clicked", G_CALLBACK(select_library_view), GINT_TO_POINTER(i)); gtk_box_pack_start(GTK_BOX(sidebar), library_nav_buttons[i], FALSE, FALSE, 0); }
 updates_nav_label = gtk_bin_get_child(GTK_BIN(library_nav_buttons[1]));
 gtk_box_pack_start(GTK_BOX(sidebar), gtk_separator_new(GTK_ORIENTATION_HORIZONTAL), FALSE, FALSE, 10);
 gtk_box_pack_start(GTK_BOX(sidebar), label("SETTINGS", "appholder-section"), FALSE, FALSE, 0);
 GtkWidget *sidebar_spacer = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0); gtk_box_pack_start(GTK_BOX(sidebar), sidebar_spacer, TRUE, TRUE, 0);
 GtkWidget *settings_nav = nav_button("⚙   Preferences"); g_signal_connect(settings_nav, "clicked", G_CALLBACK(show_settings), NULL); gtk_box_pack_end(GTK_BOX(sidebar), settings_nav, FALSE, FALSE, 0);
 GtkWidget *library = gtk_box_new(GTK_ORIENTATION_VERTICAL, 12); gtk_container_set_border_width(GTK_CONTAINER(library), 26); gtk_stack_add_titled(GTK_STACK(stack), library, "library", "Applications");
 library_views=gtk_stack_new();gtk_stack_set_transition_type(GTK_STACK(library_views),GTK_STACK_TRANSITION_TYPE_SLIDE_LEFT_RIGHT);gtk_box_pack_start(GTK_BOX(library),library_views,TRUE,TRUE,0);
 GtkWidget *list_panel = gtk_box_new(GTK_ORIENTATION_VERTICAL, 14); gtk_stack_add_named(GTK_STACK(library_views),list_panel,"list");
 detail_panel = gtk_box_new(GTK_ORIENTATION_VERTICAL, 18);gtk_container_set_border_width(GTK_CONTAINER(detail_panel),20);gtk_stack_add_named(GTK_STACK(library_views),detail_panel,"details");
 GtkWidget *back_button=gtk_button_new_with_label("←  Back to applications");g_signal_connect(back_button,"clicked",G_CALLBACK(show_library_list),NULL);gtk_widget_set_halign(back_button,GTK_ALIGN_START);gtk_box_pack_start(GTK_BOX(detail_panel),back_button,FALSE,FALSE,0);
 gtk_box_pack_start(GTK_BOX(list_panel), label("Applications", "appholder-title"), FALSE, FALSE, 0);
 summary = label("Loading your library…", "appholder-subtitle"); gtk_box_pack_start(GTK_BOX(list_panel), summary, FALSE, FALSE, 0);
 GtkWidget *toolbar = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8); gtk_box_pack_start(GTK_BOX(list_panel), toolbar, FALSE, FALSE, 0);
 gtk_box_pack_start(GTK_BOX(list_panel), label("Drop an AppImage into this window to run or integrate it.", "appholder-subtitle"), FALSE, FALSE, 0);
 search = gtk_search_entry_new(); gtk_entry_set_placeholder_text(GTK_ENTRY(search), "Search applications"); gtk_box_pack_start(GTK_BOX(toolbar), search, TRUE, TRUE, 0); g_signal_connect(search, "search-changed", G_CALLBACK(search_changed), NULL);
 GtkWidget *add = gtk_button_new_with_label("Add AppImage…"); g_signal_connect(add, "clicked", G_CALLBACK(choose), NULL); gtk_box_pack_start(GTK_BOX(toolbar), add, FALSE, FALSE, 0);
 GtkWidget *check = gtk_button_new_with_label("Check for updates"); g_signal_connect(check, "clicked", G_CALLBACK(check_updates), NULL); gtk_box_pack_start(GTK_BOX(toolbar), check, FALSE, FALSE, 0);
 update_all_button = gtk_button_new_with_label("Update all"); gtk_style_context_add_class(gtk_widget_get_style_context(update_all_button), "suggested-action"); g_signal_connect(update_all_button, "clicked", G_CALLBACK(update_all), NULL); gtk_widget_set_sensitive(update_all_button, FALSE); gtk_box_pack_start(GTK_BOX(toolbar), update_all_button, FALSE, FALSE, 0);
 app_list = gtk_list_box_new(); gtk_list_box_set_selection_mode(GTK_LIST_BOX(app_list), GTK_SELECTION_SINGLE); gtk_list_box_set_filter_func(GTK_LIST_BOX(app_list), filter_app_row, NULL, NULL); gtk_list_box_set_sort_func(GTK_LIST_BOX(app_list), sort_app_rows, NULL, NULL); gtk_list_box_set_activate_on_single_click(GTK_LIST_BOX(app_list), FALSE); g_signal_connect(app_list, "row-selected", G_CALLBACK(app_selection_changed), NULL);
 empty_library=label("No AppImages found in your watched folders. Add an AppImage or update Preferences to choose folders.","appholder-subtitle");gtk_label_set_line_wrap(GTK_LABEL(empty_library),TRUE);gtk_widget_set_no_show_all(empty_library,TRUE);gtk_box_pack_start(GTK_BOX(list_panel),empty_library,FALSE,FALSE,12);
 rows_by_id = g_hash_table_new_full(g_str_hash, g_str_equal, g_free, NULL);
 GtkWidget *scroll = gtk_scrolled_window_new(NULL, NULL); gtk_scrolled_window_set_policy(GTK_SCROLLED_WINDOW(scroll), GTK_POLICY_NEVER, GTK_POLICY_AUTOMATIC); gtk_container_add(GTK_CONTAINER(scroll), app_list); gtk_box_pack_start(GTK_BOX(list_panel), scroll, TRUE, TRUE, 0);
 GtkWidget *detail_scroll = gtk_scrolled_window_new(NULL, NULL); gtk_scrolled_window_set_policy(GTK_SCROLLED_WINDOW(detail_scroll), GTK_POLICY_NEVER, GTK_POLICY_AUTOMATIC); gtk_box_pack_start(GTK_BOX(detail_panel), detail_scroll, TRUE, TRUE, 0);
 GtkWidget *detail = gtk_box_new(GTK_ORIENTATION_VERTICAL, 10); gtk_container_set_border_width(GTK_CONTAINER(detail), 12); gtk_style_context_add_class(gtk_widget_get_style_context(detail), "appholder-detail"); gtk_container_add(GTK_CONTAINER(detail_scroll), detail);
 detail_name = label("Select an application", "appholder-title"); detail_version = label("Launch, update, or restore a saved version.", "appholder-subtitle"); detail_path = label("", "appholder-subtitle"); gtk_label_set_ellipsize(GTK_LABEL(detail_path), PANGO_ELLIPSIZE_MIDDLE); gtk_label_set_selectable(GTK_LABEL(detail_path), TRUE);
 gtk_box_pack_start(GTK_BOX(detail), detail_name, FALSE, FALSE, 0); gtk_box_pack_start(GTK_BOX(detail), detail_version, FALSE, FALSE, 0); gtk_box_pack_start(GTK_BOX(detail), detail_path, FALSE, FALSE, 0);
 detail_progress = gtk_progress_bar_new(); gtk_progress_bar_set_show_text(GTK_PROGRESS_BAR(detail_progress), TRUE); gtk_box_pack_start(GTK_BOX(detail), detail_progress, FALSE, FALSE, 0);
 GtkWidget *actions = gtk_grid_new(); gtk_grid_set_row_spacing(GTK_GRID(actions), 8); gtk_grid_set_column_spacing(GTK_GRID(actions), 8); gtk_box_pack_start(GTK_BOX(detail), actions, FALSE, FALSE, 0);
 launch_button = gtk_button_new_with_label("Launch"); gtk_style_context_add_class(gtk_widget_get_style_context(launch_button), "suggested-action"); g_signal_connect(launch_button, "clicked", G_CALLBACK(launch), NULL);
 update_button = action_button("Update", 6); gtk_style_context_add_class(gtk_widget_get_style_context(update_button), "suggested-action"); rollback_button = action_button("Restore previous", 7); versions_button = action_button("Saved versions", 8); details_button = action_button("Details", 11); remove_button = action_button("Remove", 15); uninstall_button = action_button("Uninstall", 19); gtk_style_context_add_class(gtk_widget_get_style_context(uninstall_button), "destructive-action");
 GtkWidget *buttons[] = {launch_button, update_button, rollback_button, versions_button, details_button, remove_button, uninstall_button};
 for (int i = 0; i < 6; i++) { gtk_grid_attach(GTK_GRID(actions), buttons[i], i % 2, i / 2, 1, 1); gtk_widget_set_hexpand(buttons[i], TRUE); gtk_widget_set_sensitive(buttons[i], FALSE); }
 gtk_grid_attach(GTK_GRID(actions), uninstall_button, 0, 3, 2, 1); gtk_widget_set_sensitive(uninstall_button, FALSE);
 auto_update_toggle = gtk_check_button_new_with_label("Install updates automatically"); gtk_widget_set_sensitive(auto_update_toggle, FALSE); g_signal_connect(auto_update_toggle, "toggled", G_CALLBACK(auto_update_changed), NULL); gtk_box_pack_start(GTK_BOX(detail), auto_update_toggle, FALSE, FALSE, 0);
 pin_toggle = gtk_check_button_new_with_label("Keep this version · exclude from Update all"); gtk_widget_set_sensitive(pin_toggle, FALSE); g_signal_connect(pin_toggle,"toggled",G_CALLBACK(pin_changed),NULL); gtk_box_pack_start(GTK_BOX(detail),pin_toggle,FALSE,FALSE,0);
 sandbox_toggle=gtk_check_button_new_with_label("Run in sandbox · private app home, network off");gtk_widget_set_sensitive(sandbox_toggle,FALSE);g_signal_connect(sandbox_toggle,"toggled",G_CALLBACK(sandbox_changed),NULL);gtk_box_pack_start(GTK_BOX(detail),sandbox_toggle,FALSE,FALSE,0);
 sandbox_network_toggle=gtk_check_button_new_with_label("Allow network access in sandbox");gtk_widget_set_sensitive(sandbox_network_toggle,FALSE);g_signal_connect(sandbox_network_toggle,"toggled",G_CALLBACK(sandbox_changed),NULL);gtk_box_pack_start(GTK_BOX(detail),sandbox_network_toggle,FALSE,FALSE,0);
 gtk_box_pack_start(GTK_BOX(detail),label("Sandboxing uses Bubblewrap. GUI display access remains shared; some desktop integration may not work.","appholder-subtitle"),FALSE,FALSE,0);
 status = label("", "appholder-subtitle"); gtk_label_set_line_wrap(GTK_LABEL(status), TRUE); gtk_box_pack_start(GTK_BOX(library), status, FALSE, FALSE, 0);
 GtkWidget *settings = gtk_box_new(GTK_ORIENTATION_VERTICAL, 18); gtk_container_set_border_width(GTK_CONTAINER(settings), 24); GtkWidget *settings_scroll=gtk_scrolled_window_new(NULL,NULL);gtk_scrolled_window_set_policy(GTK_SCROLLED_WINDOW(settings_scroll),GTK_POLICY_NEVER,GTK_POLICY_AUTOMATIC);gtk_container_add(GTK_CONTAINER(settings_scroll),settings);gtk_stack_add_titled(GTK_STACK(stack),settings_scroll,"settings","Settings");
 gtk_box_pack_start(GTK_BOX(settings), label("Preferences", "appholder-title"), FALSE, FALSE, 0);
 gtk_box_pack_start(GTK_BOX(settings), label("Choose how AppHolder runs and where your files live.", "appholder-subtitle"), FALSE, FALSE, 0);
 gtk_box_pack_start(GTK_BOX(settings),label("Defaults for new applications",NULL),FALSE,FALSE,0);
 default_auto_update=gtk_check_button_new_with_label("Enable automatic updates");g_signal_connect(default_auto_update,"toggled",G_CALLBACK(save_app_defaults),NULL);gtk_box_pack_start(GTK_BOX(settings),default_auto_update,FALSE,FALSE,0);
 default_sandbox=gtk_check_button_new_with_label("Run in sandbox by default");g_signal_connect(default_sandbox,"toggled",G_CALLBACK(save_app_defaults),NULL);gtk_box_pack_start(GTK_BOX(settings),default_sandbox,FALSE,FALSE,0);
 default_sandbox_network=gtk_check_button_new_with_label("Allow network access for sandboxed applications");g_signal_connect(default_sandbox_network,"toggled",G_CALLBACK(save_app_defaults),NULL);gtk_box_pack_start(GTK_BOX(settings),default_sandbox_network,FALSE,FALSE,0);
 gtk_box_pack_start(GTK_BOX(settings),label("Appearance",NULL),FALSE,FALSE,0); theme_selector=gtk_combo_box_text_new();gtk_combo_box_text_append(GTK_COMBO_BOX_TEXT(theme_selector),"system","Follow system");gtk_combo_box_text_append(GTK_COMBO_BOX_TEXT(theme_selector),"light","Light");gtk_combo_box_text_append(GTK_COMBO_BOX_TEXT(theme_selector),"dark","Dark");gtk_combo_box_set_active_id(GTK_COMBO_BOX(theme_selector),"system");g_signal_connect(theme_selector,"changed",G_CALLBACK(theme_changed),NULL);gtk_box_pack_start(GTK_BOX(settings),theme_selector,FALSE,FALSE,0);
 startup_toggle = gtk_check_button_new_with_label("Run at startup"); g_signal_connect(startup_toggle, "toggled", G_CALLBACK(startup_changed), NULL); gtk_box_pack_start(GTK_BOX(settings), startup_toggle, FALSE, FALSE, 0);
 gtk_box_pack_start(GTK_BOX(settings), label("Start the background manager and tray when you log in.", "appholder-subtitle"), FALSE, FALSE, 0);
 gtk_box_pack_start(GTK_BOX(settings),label("Watched folders",NULL),FALSE,FALSE,0);
 gtk_box_pack_start(GTK_BOX(settings),label("One folder per line. The first receives imported AppImages.","appholder-subtitle"),FALSE,FALSE,0);
 GtkWidget *retention=gtk_box_new(GTK_ORIENTATION_HORIZONTAL,8);gtk_box_pack_start(GTK_BOX(retention),label("Saved versions to keep",NULL),FALSE,FALSE,0);settings_keep_versions=gtk_spin_button_new_with_range(1,100,1);gtk_spin_button_set_value(GTK_SPIN_BUTTON(settings_keep_versions),3);gtk_box_pack_start(GTK_BOX(retention),settings_keep_versions,FALSE,FALSE,0);gtk_box_pack_start(GTK_BOX(settings),retention,FALSE,FALSE,0);
 settings_folders=gtk_list_box_new();gtk_list_box_set_selection_mode(GTK_LIST_BOX(settings_folders),GTK_SELECTION_SINGLE);g_signal_connect(settings_folders,"row-selected",G_CALLBACK(folder_selection_changed),NULL);GtkWidget *folder_scroll=gtk_scrolled_window_new(NULL,NULL);gtk_widget_set_size_request(folder_scroll,-1,150);gtk_container_add(GTK_CONTAINER(folder_scroll),settings_folders);gtk_box_pack_start(GTK_BOX(settings),folder_scroll,FALSE,FALSE,0);
 GtkWidget *folder_actions=gtk_box_new(GTK_ORIENTATION_HORIZONTAL,8);GtkWidget *add_folder_button=gtk_button_new_with_label("Add folder…");g_signal_connect(add_folder_button,"clicked",G_CALLBACK(add_folder),NULL);settings_remove_folder=gtk_button_new_with_label("Remove selected");gtk_widget_set_sensitive(settings_remove_folder,FALSE);g_signal_connect(settings_remove_folder,"clicked",G_CALLBACK(remove_folder),NULL);GtkWidget *save_folder_button=gtk_button_new_with_label("Save settings");gtk_style_context_add_class(gtk_widget_get_style_context(save_folder_button),"suggested-action");g_signal_connect(save_folder_button,"clicked",G_CALLBACK(save_folders),NULL);gtk_box_pack_start(GTK_BOX(folder_actions),add_folder_button,FALSE,FALSE,0);gtk_box_pack_start(GTK_BOX(folder_actions),settings_remove_folder,FALSE,FALSE,0);gtk_box_pack_start(GTK_BOX(folder_actions),save_folder_button,FALSE,FALSE,0);gtk_box_pack_start(GTK_BOX(settings),folder_actions,FALSE,FALSE,0);
 settings_locations = label("Loading backup location…", NULL); gtk_label_set_selectable(GTK_LABEL(settings_locations), TRUE); gtk_label_set_line_wrap(GTK_LABEL(settings_locations), TRUE); gtk_box_pack_start(GTK_BOX(settings), settings_locations, FALSE, FALSE, 0);
 settings_version=label("",NULL);gtk_box_pack_start(GTK_BOX(settings),settings_version,FALSE,FALSE,0);
 GtkWidget *update_actions=gtk_box_new(GTK_ORIENTATION_HORIZONTAL,8);GtkWidget *check_holder_button=gtk_button_new_with_label("Check AppHolder updates");g_signal_connect(check_holder_button,"clicked",G_CALLBACK(holder_check_update),NULL);holder_update_button=gtk_button_new_with_label("Update AppHolder");g_signal_connect(holder_update_button,"clicked",G_CALLBACK(holder_install_update),NULL);gtk_widget_set_sensitive(holder_update_button,FALSE);gtk_box_pack_start(GTK_BOX(update_actions),check_holder_button,FALSE,FALSE,0);gtk_box_pack_start(GTK_BOX(update_actions),holder_update_button,FALSE,FALSE,0);gtk_box_pack_start(GTK_BOX(settings),update_actions,FALSE,FALSE,0);
 settings_status = label("", "appholder-subtitle"); gtk_label_set_line_wrap(GTK_LABEL(settings_status), TRUE); gtk_box_pack_start(GTK_BOX(settings), settings_status, FALSE, FALSE, 0);
 select_library_view(NULL, GINT_TO_POINTER(0));
 GtkTargetEntry drop_target = {"text/uri-list", 0, 0}; gtk_drag_dest_set(window, GTK_DEST_DEFAULT_ALL, &drop_target, 1, GDK_ACTION_COPY); g_signal_connect(window, "drag-data-received", G_CALLBACK(receive_drop), NULL);
 gtk_widget_show_all(window); gtk_widget_hide(detail_progress); setup_tray(); g_timeout_add_seconds(1, poll_library, NULL); return 1;
}
void holder_run(void) { gtk_main(); if (indicator) { g_object_unref(indicator); indicator = NULL; } }
// Begin a refresh without clearing rows, selection, or the viewport.
void holder_begin_refresh(void) {
 rebuilding = TRUE; selected_dirty = FALSE;
 if (!seen_rows) seen_rows = g_hash_table_new_full(g_str_hash, g_str_equal, g_free, NULL);
 g_hash_table_remove_all(seen_rows);
}
void holder_add(char *id, char *name, char *version, char *path, char *icon, char *update, char *fingerprint, char *progress_text, int percent, int active, int can_update, int can_rollback, int saved, int pinned, int auto_update, int can_auto_update, int sandboxed, int sandbox_network) {
 g_hash_table_add(seen_rows, g_strdup(id));
 AppRow *app = g_hash_table_lookup(rows_by_id, id);
 gboolean is_new = app == NULL;
 if (is_new) {
  app = g_new0(AppRow, 1);
  app->id = g_strdup(id);
 }
 set_string(&app->name, name); set_string(&app->version, version); set_string(&app->path, path);
 set_string(&app->icon, icon); set_string(&app->status, update); set_string(&app->fingerprint, fingerprint); set_string(&app->progress, progress_text);
 app->percent = percent; app->active = active; app->can_update = can_update; app->can_rollback = can_rollback; app->saved = saved;
 app->pinned = pinned; app->auto_update = auto_update; app->can_auto_update = can_auto_update; app->sandboxed = sandboxed; app->sandbox_network = sandbox_network;
 if (is_new) {
  GtkWidget *row = make_app_row(app);
  gtk_style_context_add_class(gtk_widget_get_style_context(row), "app-card");
  update_app_row(app);
  gtk_list_box_insert(GTK_LIST_BOX(app_list), row, -1);
  g_hash_table_insert(rows_by_id, g_strdup(id), app);
  gtk_widget_show_all(row);
 }
 update_app_row(app);
 if (selected_app == app) selected_dirty = TRUE;
}
void holder_finish(int count, int updates, int offered, int active) {
 gtk_widget_set_sensitive(update_all_button, updates > 0);
 gchar *update_label = g_strdup_printf("↻   Updates  %d", offered); gtk_label_set_text(GTK_LABEL(updates_nav_label), update_label); g_free(update_label);
 GList *rows = gtk_container_get_children(GTK_CONTAINER(app_list));
 for (GList *item = rows; item; item = item->next) {
  GtkWidget *row = item->data; AppRow *app = g_object_get_data(G_OBJECT(row), "appholder-app");
  if (!g_hash_table_contains(seen_rows, app->id)) {
   if (selected_app == app) { render_selected_app(NULL); selected_dirty = FALSE; gtk_stack_set_visible_child_name(GTK_STACK(library_views),"list"); gtk_header_bar_set_subtitle(GTK_HEADER_BAR(header_bar),library_filter?"Updates":"Applications"); }
   g_hash_table_remove(rows_by_id, app->id);
   gtk_widget_destroy(row);
  }
 }
 g_list_free(rows);
 rebuilding = FALSE;
 gchar *text = g_strdup_printf("%d applications · %d updates available · %d active tasks", count, updates, active);
 if (g_strcmp0(text, gtk_label_get_text(GTK_LABEL(summary)))) gtk_label_set_text(GTK_LABEL(summary), text);
 g_free(text);
 if(empty_library){if(count==0)gtk_widget_show(empty_library);else gtk_widget_hide(empty_library);}
 gtk_list_box_invalidate_filter(GTK_LIST_BOX(app_list)); gtk_list_box_invalidate_sort(GTK_LIST_BOX(app_list));
 if (selected_dirty && selected_app) render_selected_app(selected_app);
}
void holder_status(char *text) { gtk_label_set_text(GTK_LABEL(status), text); }
void holder_settings(char *folders,char *backup,int keep_versions) {set_folder_list(folders);gtk_spin_button_set_value(GTK_SPIN_BUTTON(settings_keep_versions),keep_versions);gchar *text=g_strdup_printf("Backups\n%s",backup);gtk_label_set_text(GTK_LABEL(settings_locations),text);g_free(text);}
void holder_settings_keep(int keep_versions){gtk_spin_button_set_value(GTK_SPIN_BUTTON(settings_keep_versions),keep_versions);}
void holder_version(char *text){gtk_label_set_text(GTK_LABEL(settings_version),text);}
void holder_update_available(int available){gtk_widget_set_sensitive(holder_update_button,available);}
void holder_settings_status(char *text) { gtk_label_set_text(GTK_LABEL(settings_status), text); }

void holder_details(char *name, char *text) {
 GtkWidget *dialog = gtk_dialog_new_with_buttons(name, GTK_WINDOW(window), GTK_DIALOG_MODAL | GTK_DIALOG_DESTROY_WITH_PARENT, "Close", GTK_RESPONSE_CLOSE, NULL);
 gtk_window_set_default_size(GTK_WINDOW(dialog), 680, 560);
 GtkWidget *area = gtk_dialog_get_content_area(GTK_DIALOG(dialog)); gtk_container_set_border_width(GTK_CONTAINER(area), 20);
 GtkWidget *scroll = gtk_scrolled_window_new(NULL, NULL); gtk_widget_set_vexpand(scroll, TRUE); gtk_box_pack_start(GTK_BOX(area), scroll, TRUE, TRUE, 0);
 GtkWidget *view = gtk_text_view_new(); gtk_text_view_set_editable(GTK_TEXT_VIEW(view), FALSE); gtk_text_view_set_cursor_visible(GTK_TEXT_VIEW(view), FALSE); gtk_text_view_set_wrap_mode(GTK_TEXT_VIEW(view), GTK_WRAP_WORD_CHAR); gtk_text_view_set_left_margin(GTK_TEXT_VIEW(view), 12); gtk_text_view_set_right_margin(GTK_TEXT_VIEW(view), 12);
 gtk_text_buffer_set_text(gtk_text_view_get_buffer(GTK_TEXT_VIEW(view)), text, -1); gtk_container_add(GTK_CONTAINER(scroll), view);
 gtk_widget_show_all(dialog); gtk_dialog_run(GTK_DIALOG(dialog)); gtk_widget_destroy(dialog);
}

void holder_settings_folders(char *folders){set_folder_list(folders);}
