//go:build desktop && linux && cgo

#include "gui.h"
#include <dlfcn.h>
extern void goHolderAction(int action, char *value);
extern void goHolderDrain(void);
#include <string.h>
static GtkWidget *window, *tree, *status;
static GtkListStore *store;
static GtkWidget *summary, *detail_name, *detail_version, *detail_path, *detail_progress;
static GtkWidget *update_all_button, *pin_toggle, *sandbox_toggle, *sandbox_network_toggle;
static gboolean setting_pin = FALSE;
static gboolean setting_sandbox = FALSE;
static GtkWidget *launch_button, *update_button, *rollback_button, *versions_button, *details_button, *remove_button, *uninstall_button, *search;
static GtkTreeModelFilter *filter_model;
static gchar *selected_id;
static gboolean rebuilding = FALSE;
static GHashTable *seen_rows;
static gboolean selected_dirty = FALSE;
static GObject *indicator;
static void *indicator_library;
static gboolean tray_connected = FALSE;
static gboolean start_in_tray = FALSE;
static gboolean user_opened_library = FALSE;
static GtkWidget *startup_toggle, *settings_locations, *settings_status, *settings_folders, *settings_version, *holder_update_button, *settings_keep_versions;
static gboolean setting_startup = FALSE;
static void startup_changed(GtkToggleButton *button, gpointer data) { if (!setting_startup) goHolderAction(9, gtk_toggle_button_get_active(button) ? "enable" : "disable"); }
static void sandbox_changed(GtkToggleButton *button, gpointer data) { if (!setting_sandbox && selected_id) { gchar *request=g_strdup_printf("%s|%d|%d",selected_id,gtk_toggle_button_get_active(GTK_TOGGLE_BUTTON(sandbox_toggle)),gtk_toggle_button_get_active(GTK_TOGGLE_BUTTON(sandbox_network_toggle)));goHolderAction(23,request);g_free(request); } }
void holder_startup_state(int enabled) { setting_startup = TRUE; gtk_toggle_button_set_active(GTK_TOGGLE_BUTTON(startup_toggle), enabled); setting_startup = FALSE; }
static void show_library(GtkMenuItem *item, gpointer data) {
 user_opened_library = TRUE;
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
 GtkTreeSelection *selection = gtk_tree_view_get_selection(GTK_TREE_VIEW(tree));
 GtkTreeIter iter; GtkTreeModel *model;
 if (gtk_tree_selection_get_selected(selection, &model, &iter)) {
  gchar *id; gtk_tree_model_get(model, &iter, 0, &id, -1);
  goHolderAction(1, id); g_free(id);
 } else holder_status("Select an application first.");
}
static void update_all(GtkButton *button, gpointer data) {
 GtkWidget *dialog = gtk_message_dialog_new(GTK_WINDOW(window), GTK_DIALOG_MODAL, GTK_MESSAGE_QUESTION, GTK_BUTTONS_OK_CANCEL, "Install all available updates?");
 gtk_message_dialog_format_secondary_text(GTK_MESSAGE_DIALOG(dialog), "Versions marked Keep this version are excluded. Each updated file is backed up.");
 int answer = gtk_dialog_run(GTK_DIALOG(dialog)); gtk_widget_destroy(dialog); if (answer == GTK_RESPONSE_OK) goHolderAction(12, "");
}
static void pin_changed(GtkToggleButton *button, gpointer data) { if (!setting_pin && selected_id) goHolderAction(gtk_toggle_button_get_active(button) ? 13 : 14, selected_id); }
static void auto_update_toggled(GtkCellRendererToggle *cell, gchar *path_text, gpointer data) {
 GtkTreeIter iter; GtkTreePath *path=gtk_tree_path_new_from_string(path_text);GtkTreeModel *model=gtk_tree_view_get_model(GTK_TREE_VIEW(tree));
 if(gtk_tree_model_get_iter(model,&iter,path)){gchar *id;gboolean enabled,allowed;gtk_tree_model_get(model,&iter,0,&id,16,&enabled,17,&allowed,-1);
  if(allowed){GtkTreeIter child;gtk_tree_model_filter_convert_iter_to_child_iter(filter_model,&child,&iter);gtk_list_store_set(store,&child,16,!enabled,-1);goHolderAction(enabled?21:20,id);}g_free(id);
 }gtk_tree_path_free(path);
}
static void selected_action(GtkButton *button, gpointer data) {
 GtkTreeSelection *selection = gtk_tree_view_get_selection(GTK_TREE_VIEW(tree));
 GtkTreeIter iter; GtkTreeModel *model; int action = GPOINTER_TO_INT(data);
 if (!gtk_tree_selection_get_selected(selection, &model, &iter)) { holder_status("Select an application first."); return; }
 gchar *id; gtk_tree_model_get(model, &iter, 0, &id, -1);
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
  GtkWidget *question = gtk_message_dialog_new(GTK_WINDOW(window), GTK_DIALOG_MODAL, GTK_MESSAGE_QUESTION, GTK_BUTTONS_NONE, "What would you like to do with this AppImage?");
  gtk_message_dialog_format_secondary_text(GTK_MESSAGE_DIALOG(question), "%s", path);
  gtk_dialog_add_buttons(GTK_DIALOG(question), "Cancel", GTK_RESPONSE_CANCEL, "Run once", 1, "Integrate", 2, "Integrate and run", 3, NULL);
  int answer = gtk_dialog_run(GTK_DIALOG(question)); gtk_widget_destroy(question);
  if (answer >= 1 && answer <= 3) goHolderAction(answer + 1, path);
}
static GtkWidget *label(const char *text, const char *style) {
 GtkWidget *widget = gtk_label_new(text); gtk_widget_set_halign(widget, GTK_ALIGN_START);
 if (style) gtk_style_context_add_class(gtk_widget_get_style_context(widget), style);
 return widget;
}
static gboolean visible_row(GtkTreeModel *model, GtkTreeIter *iter, gpointer data) {
 const gchar *query = gtk_entry_get_text(GTK_ENTRY(search)); if (!query[0]) return TRUE;
 gchar *name, *path; gtk_tree_model_get(model, iter, 2, &name, 4, &path, -1);
 gchar *haystack = g_utf8_casefold(name, -1), *needle = g_utf8_casefold(query, -1), *location = g_utf8_casefold(path, -1);
 gboolean found = strstr(haystack, needle) != NULL || strstr(location, needle) != NULL;
 g_free(name); g_free(path); g_free(haystack); g_free(needle); g_free(location); return found;
}
static void selection_changed(GtkTreeSelection *selection, gpointer data) {
 if (rebuilding) return;
 GtkTreeIter iter; GtkTreeModel *model;
 gboolean has = gtk_tree_selection_get_selected(selection, &model, &iter);
 gtk_widget_set_sensitive(pin_toggle, has);
 gtk_widget_set_sensitive(sandbox_toggle, has);
 gtk_widget_set_sensitive(details_button, has); gtk_widget_set_sensitive(remove_button, has); gtk_widget_set_sensitive(uninstall_button, has);
 gtk_widget_set_sensitive(launch_button, has); gtk_widget_set_sensitive(update_button, FALSE);
 gtk_widget_set_sensitive(rollback_button, FALSE); gtk_widget_set_sensitive(versions_button, FALSE);
 g_free(selected_id); selected_id = NULL;
 if (!has) { gtk_label_set_text(GTK_LABEL(detail_name), "Select an application"); gtk_label_set_text(GTK_LABEL(detail_version), "Launch, update, or restore a saved version."); gtk_label_set_text(GTK_LABEL(detail_path), ""); gtk_widget_hide(detail_progress); return; }
 gchar *name, *version, *path, *state, *progress_text; gint percent, saved, pulse; gboolean active, can_update, can_rollback, pinned, sandboxed, sandbox_network;
 gtk_tree_model_get(model, &iter, 0, &selected_id, 2, &name, 3, &version, 4, &path, 5, &state, 6, &percent, 7, &active, 8, &pulse, 9, &can_update, 10, &can_rollback, 11, &saved, 14, &pinned, 15, &progress_text, 18, &sandboxed, 19, &sandbox_network, -1);
 setting_pin = TRUE; gtk_toggle_button_set_active(GTK_TOGGLE_BUTTON(pin_toggle), pinned); setting_pin = FALSE;
 setting_sandbox=TRUE;gtk_toggle_button_set_active(GTK_TOGGLE_BUTTON(sandbox_toggle),sandboxed);gtk_toggle_button_set_active(GTK_TOGGLE_BUTTON(sandbox_network_toggle),sandbox_network);gtk_widget_set_sensitive(sandbox_network_toggle,sandboxed);setting_sandbox=FALSE;
 gtk_label_set_text(GTK_LABEL(detail_name), name);
 gchar *description = g_strdup_printf("Version %s · %s", version, state); gtk_label_set_text(GTK_LABEL(detail_version), description);
 gtk_label_set_text(GTK_LABEL(detail_path), path); gtk_widget_set_tooltip_text(detail_path, path);
 gtk_widget_set_sensitive(remove_button, !active); gtk_widget_set_sensitive(uninstall_button, !active);
 gtk_widget_set_sensitive(update_button, can_update && !active); gtk_widget_set_sensitive(rollback_button, can_rollback && !active); gtk_widget_set_sensitive(versions_button, saved > 0);
 if (active) { gtk_widget_show(detail_progress); gtk_progress_bar_set_text(GTK_PROGRESS_BAR(detail_progress), progress_text); if (pulse < 0) gtk_progress_bar_set_fraction(GTK_PROGRESS_BAR(detail_progress), percent / 100.0); else gtk_progress_bar_pulse(GTK_PROGRESS_BAR(detail_progress)); } else gtk_widget_hide(detail_progress);
 g_free(name); g_free(version); g_free(path); g_free(state); g_free(progress_text); g_free(description);
}
static void search_changed(GtkSearchEntry *entry, gpointer data) { gtk_tree_model_filter_refilter(filter_model); selection_changed(gtk_tree_view_get_selection(GTK_TREE_VIEW(tree)), NULL); }
static gboolean poll_library(gpointer data) { goHolderAction(0, ""); return G_SOURCE_CONTINUE; }
static gboolean drain_events(gpointer data) { goHolderDrain(); return G_SOURCE_REMOVE; }
void holder_wake(void) { g_idle_add(drain_events, NULL); }
static GtkWidget *action_button(const char *text, int action) {
 GtkWidget *button = gtk_button_new_with_label(text); g_signal_connect(button, "clicked", G_CALLBACK(selected_action), GINT_TO_POINTER(action)); return button;
}
static void save_folders(GtkButton *button, gpointer data) {
 GtkTextBuffer *buffer = gtk_text_view_get_buffer(GTK_TEXT_VIEW(settings_folders)); GtkTextIter start,end; gtk_text_buffer_get_bounds(buffer,&start,&end);
 gchar *text=gtk_text_buffer_get_text(buffer,&start,&end,FALSE);gchar *request=g_strdup_printf("%s\n--KEEP--\n%d",text,gtk_spin_button_get_value_as_int(GTK_SPIN_BUTTON(settings_keep_versions)));goHolderAction(16,request);g_free(request);g_free(text);
}
static void add_folder(GtkButton *button, gpointer data) {
 GtkWidget *dialog=gtk_file_chooser_dialog_new("Add watched folder",GTK_WINDOW(window),GTK_FILE_CHOOSER_ACTION_SELECT_FOLDER,"Cancel",GTK_RESPONSE_CANCEL,"Add",GTK_RESPONSE_ACCEPT,NULL);
 if(gtk_dialog_run(GTK_DIALOG(dialog))==GTK_RESPONSE_ACCEPT){gchar *path=gtk_file_chooser_get_filename(GTK_FILE_CHOOSER(dialog));GtkTextBuffer *buffer=gtk_text_view_get_buffer(GTK_TEXT_VIEW(settings_folders));GtkTextIter end;gtk_text_buffer_get_end_iter(buffer,&end);if(gtk_text_buffer_get_char_count(buffer)>0)gtk_text_buffer_insert(buffer,&end,"\n",-1);gtk_text_buffer_insert(buffer,&end,path,-1);g_free(path);}gtk_widget_destroy(dialog);
}
static void holder_check_update(GtkButton *button,gpointer data){goHolderAction(17,"");}
static void holder_install_update(GtkButton *button,gpointer data){GtkWidget *dialog=gtk_message_dialog_new(GTK_WINDOW(window),GTK_DIALOG_MODAL,GTK_MESSAGE_QUESTION,GTK_BUTTONS_OK_CANCEL,"Update AppHolder?");gtk_message_dialog_format_secondary_text(GTK_MESSAGE_DIALOG(dialog),"Administrator authorization may be required. Restart AppHolder after installation.");int answer=gtk_dialog_run(GTK_DIALOG(dialog));gtk_widget_destroy(dialog);if(answer==GTK_RESPONSE_OK)goHolderAction(18,"");}
int holder_init(void) {
 if (!gtk_init_check(NULL, NULL)) return 0;
 GtkCssProvider *css = gtk_css_provider_new();
 gtk_css_provider_load_from_data(css, ".appholder-title { font-size: 24px; font-weight: bold; } .appholder-subtitle { opacity: 0.68; } .appholder-detail { padding: 20px; background-color: alpha(#8C939B,0.12); border: 1px solid alpha(#4A5260,0.18); border-radius: 12px; } .appholder-sidebar { background-color: alpha(#1E2227,0.07); padding: 16px; } treeview { padding: 6px; } treeview.view { border-radius: 10px; } treeview.view:selected { background-color: #3A6EA5; } button { padding: 6px 12px; border-radius: 7px; } button.suggested-action { background-image: none; background-color: #3A6EA5; color: #F4F2EE; border-color: #3A6EA5; } button.suggested-action:hover { background-color: #315F8F; }", -1, NULL);
 gtk_style_context_add_provider_for_screen(gdk_screen_get_default(), GTK_STYLE_PROVIDER(css), GTK_STYLE_PROVIDER_PRIORITY_APPLICATION); g_object_unref(css);
 window = gtk_window_new(GTK_WINDOW_TOPLEVEL); gtk_window_set_title(GTK_WINDOW(window), "AppHolder"); gtk_window_set_icon_name(GTK_WINDOW(window), "appholder"); gtk_window_set_default_size(GTK_WINDOW(window), 1100, 720);
 g_signal_connect(window, "delete-event", G_CALLBACK(close_library), NULL); g_signal_connect(window, "destroy", G_CALLBACK(gtk_main_quit), NULL);
 GtkWidget *header = gtk_header_bar_new(); gtk_header_bar_set_title(GTK_HEADER_BAR(header), "AppHolder"); gtk_header_bar_set_show_close_button(GTK_HEADER_BAR(header), TRUE); gtk_window_set_titlebar(GTK_WINDOW(window), header);
 GtkWidget *body = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 0); gtk_container_add(GTK_CONTAINER(window), body);
 GtkWidget *stack = gtk_stack_new(); gtk_stack_set_transition_type(GTK_STACK(stack), GTK_STACK_TRANSITION_TYPE_CROSSFADE);
 GtkWidget *sidebar = gtk_stack_sidebar_new(); gtk_stack_sidebar_set_stack(GTK_STACK_SIDEBAR(sidebar), GTK_STACK(stack)); gtk_widget_set_size_request(sidebar, 180, -1); gtk_style_context_add_class(gtk_widget_get_style_context(sidebar), "appholder-sidebar"); gtk_box_pack_start(GTK_BOX(body), sidebar, FALSE, FALSE, 0); gtk_box_pack_start(GTK_BOX(body), stack, TRUE, TRUE, 0);
 GtkWidget *library = gtk_box_new(GTK_ORIENTATION_VERTICAL, 16); gtk_container_set_border_width(GTK_CONTAINER(library), 24); gtk_stack_add_titled(GTK_STACK(stack), library, "library", "Applications");
 gtk_box_pack_start(GTK_BOX(library), label("Your applications", "appholder-title"), FALSE, FALSE, 0);
 summary = label("Loading your library…", "appholder-subtitle"); gtk_box_pack_start(GTK_BOX(library), summary, FALSE, FALSE, 0);
 GtkWidget *toolbar = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8); gtk_box_pack_start(GTK_BOX(library), toolbar, FALSE, FALSE, 0);
 search = gtk_search_entry_new(); gtk_entry_set_placeholder_text(GTK_ENTRY(search), "Search applications"); gtk_box_pack_start(GTK_BOX(toolbar), search, TRUE, TRUE, 0); g_signal_connect(search, "search-changed", G_CALLBACK(search_changed), NULL);
 GtkWidget *add = gtk_button_new_with_label("Add AppImage…"); g_signal_connect(add, "clicked", G_CALLBACK(choose), NULL); gtk_box_pack_start(GTK_BOX(toolbar), add, FALSE, FALSE, 0);
 GtkWidget *check = gtk_button_new_with_label("Check for updates"); g_signal_connect(check, "clicked", G_CALLBACK(check_updates), NULL); gtk_box_pack_start(GTK_BOX(toolbar), check, FALSE, FALSE, 0);
 update_all_button = gtk_button_new_with_label("Update all"); gtk_style_context_add_class(gtk_widget_get_style_context(update_all_button), "suggested-action"); g_signal_connect(update_all_button, "clicked", G_CALLBACK(update_all), NULL); gtk_widget_set_sensitive(update_all_button, FALSE); gtk_box_pack_start(GTK_BOX(toolbar), update_all_button, FALSE, FALSE, 0);
 store = gtk_list_store_new(20, G_TYPE_STRING, GDK_TYPE_PIXBUF, G_TYPE_STRING, G_TYPE_STRING, G_TYPE_STRING, G_TYPE_STRING, G_TYPE_INT, G_TYPE_BOOLEAN, G_TYPE_INT, G_TYPE_BOOLEAN, G_TYPE_BOOLEAN, G_TYPE_INT, G_TYPE_STRING, G_TYPE_STRING, G_TYPE_BOOLEAN, G_TYPE_STRING, G_TYPE_BOOLEAN, G_TYPE_BOOLEAN, G_TYPE_BOOLEAN, G_TYPE_BOOLEAN);
 gtk_tree_sortable_set_sort_column_id(GTK_TREE_SORTABLE(store), 2, GTK_SORT_ASCENDING);
 filter_model = GTK_TREE_MODEL_FILTER(gtk_tree_model_filter_new(GTK_TREE_MODEL(store), NULL)); gtk_tree_model_filter_set_visible_func(filter_model, visible_row, NULL, NULL);
 tree = gtk_tree_view_new_with_model(GTK_TREE_MODEL(filter_model)); g_object_unref(filter_model); g_object_unref(store);
 GtkCellRenderer *renderer = gtk_cell_renderer_pixbuf_new(); gtk_tree_view_insert_column_with_attributes(GTK_TREE_VIEW(tree), -1, "", renderer, "pixbuf", 1, NULL);
 const char *titles[] = {"Application", "Version", "Status"}; int columns[] = {2, 3, 5};
 for (int i = 0; i < 3; i++) { renderer = gtk_cell_renderer_text_new(); g_object_set(renderer, "ypad", 12, "ellipsize", PANGO_ELLIPSIZE_END, NULL); GtkTreeViewColumn *column = gtk_tree_view_column_new_with_attributes(titles[i], renderer, "text", columns[i], NULL); gtk_tree_view_column_set_expand(column, i != 1); gtk_tree_view_append_column(GTK_TREE_VIEW(tree), column); }

 renderer=gtk_cell_renderer_toggle_new();g_signal_connect(renderer,"toggled",G_CALLBACK(auto_update_toggled),NULL);gtk_tree_view_insert_column_with_attributes(GTK_TREE_VIEW(tree),-1,"Auto-update",renderer,"active",16,"activatable",17,"sensitive",17,NULL);
 GtkWidget *scroll = gtk_scrolled_window_new(NULL, NULL); gtk_container_add(GTK_CONTAINER(scroll), tree); gtk_box_pack_start(GTK_BOX(library), scroll, TRUE, TRUE, 0);
 GtkWidget *detail = gtk_box_new(GTK_ORIENTATION_VERTICAL, 8); gtk_style_context_add_class(gtk_widget_get_style_context(detail), "appholder-detail"); gtk_box_pack_start(GTK_BOX(library), detail, FALSE, FALSE, 0);
 detail_name = label("Select an application", "appholder-title"); detail_version = label("Launch, update, or restore a saved version.", "appholder-subtitle"); detail_path = label("", "appholder-subtitle"); gtk_label_set_ellipsize(GTK_LABEL(detail_path), PANGO_ELLIPSIZE_MIDDLE); gtk_label_set_selectable(GTK_LABEL(detail_path), TRUE);
 gtk_box_pack_start(GTK_BOX(detail), detail_name, FALSE, FALSE, 0); gtk_box_pack_start(GTK_BOX(detail), detail_version, FALSE, FALSE, 0); gtk_box_pack_start(GTK_BOX(detail), detail_path, FALSE, FALSE, 0);
 detail_progress = gtk_progress_bar_new(); gtk_progress_bar_set_show_text(GTK_PROGRESS_BAR(detail_progress), TRUE); gtk_box_pack_start(GTK_BOX(detail), detail_progress, FALSE, FALSE, 0);
 GtkWidget *actions = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8); gtk_box_pack_start(GTK_BOX(detail), actions, FALSE, FALSE, 0);
 launch_button = gtk_button_new_with_label("Launch"); gtk_style_context_add_class(gtk_widget_get_style_context(launch_button), "suggested-action"); g_signal_connect(launch_button, "clicked", G_CALLBACK(launch), NULL);
 update_button = action_button("Update", 6); gtk_style_context_add_class(gtk_widget_get_style_context(update_button), "suggested-action"); rollback_button = action_button("Restore previous", 7); versions_button = action_button("Saved versions", 8); details_button = action_button("Details", 11); remove_button = action_button("Remove", 15); uninstall_button = action_button("Uninstall", 19); gtk_style_context_add_class(gtk_widget_get_style_context(uninstall_button), "destructive-action");
 GtkWidget *buttons[] = {launch_button, update_button, rollback_button, versions_button, details_button, remove_button, uninstall_button}; for (int i = 0; i < 7; i++) { gtk_box_pack_start(GTK_BOX(actions), buttons[i], FALSE, FALSE, 0); gtk_widget_set_sensitive(buttons[i], FALSE); }
 pin_toggle = gtk_check_button_new_with_label("Keep this version · exclude from Update all"); gtk_widget_set_sensitive(pin_toggle, FALSE); g_signal_connect(pin_toggle,"toggled",G_CALLBACK(pin_changed),NULL); gtk_box_pack_start(GTK_BOX(detail),pin_toggle,FALSE,FALSE,0);
 sandbox_toggle=gtk_check_button_new_with_label("Run in sandbox · private app home, network off");gtk_widget_set_sensitive(sandbox_toggle,FALSE);g_signal_connect(sandbox_toggle,"toggled",G_CALLBACK(sandbox_changed),NULL);gtk_box_pack_start(GTK_BOX(detail),sandbox_toggle,FALSE,FALSE,0);
 sandbox_network_toggle=gtk_check_button_new_with_label("Allow network access in sandbox");gtk_widget_set_sensitive(sandbox_network_toggle,FALSE);g_signal_connect(sandbox_network_toggle,"toggled",G_CALLBACK(sandbox_changed),NULL);gtk_box_pack_start(GTK_BOX(detail),sandbox_network_toggle,FALSE,FALSE,0);
 gtk_box_pack_start(GTK_BOX(detail),label("Sandboxing uses Bubblewrap. GUI display access remains shared; some desktop integration may not work.","appholder-subtitle"),FALSE,FALSE,0);
 g_signal_connect(gtk_tree_view_get_selection(GTK_TREE_VIEW(tree)), "changed", G_CALLBACK(selection_changed), NULL);
 status = label("", "appholder-subtitle"); gtk_label_set_line_wrap(GTK_LABEL(status), TRUE); gtk_box_pack_start(GTK_BOX(library), status, FALSE, FALSE, 0);
 GtkWidget *settings = gtk_box_new(GTK_ORIENTATION_VERTICAL, 18); gtk_container_set_border_width(GTK_CONTAINER(settings), 24); GtkWidget *settings_scroll=gtk_scrolled_window_new(NULL,NULL);gtk_scrolled_window_set_policy(GTK_SCROLLED_WINDOW(settings_scroll),GTK_POLICY_NEVER,GTK_POLICY_AUTOMATIC);gtk_container_add(GTK_CONTAINER(settings_scroll),settings);gtk_stack_add_titled(GTK_STACK(stack),settings_scroll,"settings","Settings");
 gtk_box_pack_start(GTK_BOX(settings), label("Settings", "appholder-title"), FALSE, FALSE, 0);
 gtk_box_pack_start(GTK_BOX(settings), label("Choose how AppHolder runs and where your files live.", "appholder-subtitle"), FALSE, FALSE, 0);
 startup_toggle = gtk_check_button_new_with_label("Run at startup"); g_signal_connect(startup_toggle, "toggled", G_CALLBACK(startup_changed), NULL); gtk_box_pack_start(GTK_BOX(settings), startup_toggle, FALSE, FALSE, 0);
 gtk_box_pack_start(GTK_BOX(settings), label("Start the background manager and tray when you log in.", "appholder-subtitle"), FALSE, FALSE, 0);
 gtk_box_pack_start(GTK_BOX(settings),label("Watched folders",NULL),FALSE,FALSE,0);
 gtk_box_pack_start(GTK_BOX(settings),label("One folder per line. The first receives imported AppImages.","appholder-subtitle"),FALSE,FALSE,0);
 GtkWidget *retention=gtk_box_new(GTK_ORIENTATION_HORIZONTAL,8);gtk_box_pack_start(GTK_BOX(retention),label("Saved versions to keep",NULL),FALSE,FALSE,0);settings_keep_versions=gtk_spin_button_new_with_range(1,100,1);gtk_spin_button_set_value(GTK_SPIN_BUTTON(settings_keep_versions),3);gtk_box_pack_start(GTK_BOX(retention),settings_keep_versions,FALSE,FALSE,0);gtk_box_pack_start(GTK_BOX(settings),retention,FALSE,FALSE,0);
 settings_folders=gtk_text_view_new();gtk_text_view_set_wrap_mode(GTK_TEXT_VIEW(settings_folders),GTK_WRAP_NONE);GtkWidget *folder_scroll=gtk_scrolled_window_new(NULL,NULL);gtk_widget_set_size_request(folder_scroll,-1,120);gtk_container_add(GTK_CONTAINER(folder_scroll),settings_folders);gtk_box_pack_start(GTK_BOX(settings),folder_scroll,FALSE,FALSE,0);
 GtkWidget *folder_actions=gtk_box_new(GTK_ORIENTATION_HORIZONTAL,8);GtkWidget *add_folder_button=gtk_button_new_with_label("Add folder…");g_signal_connect(add_folder_button,"clicked",G_CALLBACK(add_folder),NULL);GtkWidget *save_folder_button=gtk_button_new_with_label("Save settings");gtk_style_context_add_class(gtk_widget_get_style_context(save_folder_button),"suggested-action");g_signal_connect(save_folder_button,"clicked",G_CALLBACK(save_folders),NULL);gtk_box_pack_start(GTK_BOX(folder_actions),add_folder_button,FALSE,FALSE,0);gtk_box_pack_start(GTK_BOX(folder_actions),save_folder_button,FALSE,FALSE,0);gtk_box_pack_start(GTK_BOX(settings),folder_actions,FALSE,FALSE,0);
 settings_locations = label("Loading backup location…", NULL); gtk_label_set_selectable(GTK_LABEL(settings_locations), TRUE); gtk_label_set_line_wrap(GTK_LABEL(settings_locations), TRUE); gtk_box_pack_start(GTK_BOX(settings), settings_locations, FALSE, FALSE, 0);
 settings_version=label("",NULL);gtk_box_pack_start(GTK_BOX(settings),settings_version,FALSE,FALSE,0);
 GtkWidget *update_actions=gtk_box_new(GTK_ORIENTATION_HORIZONTAL,8);GtkWidget *check_holder_button=gtk_button_new_with_label("Check AppHolder updates");g_signal_connect(check_holder_button,"clicked",G_CALLBACK(holder_check_update),NULL);holder_update_button=gtk_button_new_with_label("Update AppHolder");g_signal_connect(holder_update_button,"clicked",G_CALLBACK(holder_install_update),NULL);gtk_widget_set_sensitive(holder_update_button,FALSE);gtk_box_pack_start(GTK_BOX(update_actions),check_holder_button,FALSE,FALSE,0);gtk_box_pack_start(GTK_BOX(update_actions),holder_update_button,FALSE,FALSE,0);gtk_box_pack_start(GTK_BOX(settings),update_actions,FALSE,FALSE,0);
 settings_status = label("", "appholder-subtitle"); gtk_label_set_line_wrap(GTK_LABEL(settings_status), TRUE); gtk_box_pack_start(GTK_BOX(settings), settings_status, FALSE, FALSE, 0);
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
 GtkTreeIter iter; gboolean found = FALSE;
 for (gboolean valid = gtk_tree_model_get_iter_first(GTK_TREE_MODEL(store), &iter); valid; valid = gtk_tree_model_iter_next(GTK_TREE_MODEL(store), &iter)) {
  gchar *existing_id; gtk_tree_model_get(GTK_TREE_MODEL(store), &iter, 0, &existing_id, -1);
  found = !g_strcmp0(existing_id, id); g_free(existing_id); if (found) break;
 }
 int value = percent < 0 ? 0 : percent;
 int pulse = active && percent < 0 ? (int)(g_get_monotonic_time() / 100000 % 20) : -1;
 gboolean reload_icon = !found;
 if (found) {
  gchar *old_name, *old_version, *old_path, *old_update, *old_icon, *old_fingerprint, *old_progress_text;
  gint old_value, old_pulse, old_saved; gboolean old_active, old_can_update, old_can_rollback, old_pinned, old_auto_update, old_can_auto_update, old_sandboxed, old_sandbox_network;
  gtk_tree_model_get(GTK_TREE_MODEL(store), &iter, 2, &old_name, 3, &old_version, 4, &old_path, 5, &old_update, 6, &old_value, 7, &old_active, 8, &old_pulse, 9, &old_can_update, 10, &old_can_rollback, 11, &old_saved, 12, &old_icon, 13, &old_fingerprint, 14, &old_pinned, 15, &old_progress_text, 16, &old_auto_update, 17, &old_can_auto_update, 18, &old_sandboxed, 19, &old_sandbox_network, -1);
  reload_icon = g_strcmp0(icon, old_icon) || g_strcmp0(fingerprint, old_fingerprint);
  gboolean changed = reload_icon || g_strcmp0(name, old_name) || g_strcmp0(version, old_version) || g_strcmp0(path, old_path) || g_strcmp0(update, old_update) || value != old_value || active != old_active || pulse != old_pulse || can_update != old_can_update || can_rollback != old_can_rollback || saved != old_saved || pinned != old_pinned || g_strcmp0(progress_text, old_progress_text) || auto_update != old_auto_update || can_auto_update != old_can_auto_update || sandboxed != old_sandboxed || sandbox_network != old_sandbox_network;
  g_free(old_name); g_free(old_version); g_free(old_path); g_free(old_update); g_free(old_icon); g_free(old_fingerprint); g_free(old_progress_text);
  if (!changed) return;
 } else gtk_list_store_append(store, &iter);
 gtk_list_store_set(store, &iter, 0, id, 2, name, 3, version, 4, path, 5, update, 6, value, 7, active, 8, pulse, 9, can_update, 10, can_rollback, 11, saved, 12, icon, 13, fingerprint, 14, pinned, 15, progress_text, 16, auto_update, 17, can_auto_update, 18, sandboxed, 19, sandbox_network, -1);
 if (reload_icon) {
  GdkPixbuf *pixbuf = NULL; if (icon[0]) pixbuf = gdk_pixbuf_new_from_file_at_scale(icon, 32, 32, TRUE, NULL);
  if (!pixbuf) pixbuf = gtk_icon_theme_load_icon(gtk_icon_theme_get_default(), "application-x-executable", 32, 0, NULL);
  gtk_list_store_set(store, &iter, 1, pixbuf, -1); if (pixbuf) g_object_unref(pixbuf);
 }
 if (selected_id && !g_strcmp0(selected_id, id)) selected_dirty = TRUE;
}
void holder_finish(int count, int updates, int active) {
 gtk_widget_set_sensitive(update_all_button, updates > 0);
 GtkTreeIter iter; gboolean valid = gtk_tree_model_get_iter_first(GTK_TREE_MODEL(store), &iter);
 while (valid) {
  gchar *id; gtk_tree_model_get(GTK_TREE_MODEL(store), &iter, 0, &id, -1);
  if (!g_hash_table_contains(seen_rows, id)) { if (!g_strcmp0(selected_id, id)) selected_dirty = TRUE; valid = gtk_list_store_remove(store, &iter); }
  else valid = gtk_tree_model_iter_next(GTK_TREE_MODEL(store), &iter);
  g_free(id);
 }
 rebuilding = FALSE;
 gchar *text = g_strdup_printf("%d applications · %d updates available · %d active tasks", count, updates, active);
 if (g_strcmp0(text, gtk_label_get_text(GTK_LABEL(summary)))) gtk_label_set_text(GTK_LABEL(summary), text);
 g_free(text);
 if (selected_dirty) selection_changed(gtk_tree_view_get_selection(GTK_TREE_VIEW(tree)), NULL);
}
void holder_status(char *text) { gtk_label_set_text(GTK_LABEL(status), text); }
void holder_settings(char *folders,char *backup,int keep_versions) {gtk_text_buffer_set_text(gtk_text_view_get_buffer(GTK_TEXT_VIEW(settings_folders)),folders,-1);gtk_spin_button_set_value(GTK_SPIN_BUTTON(settings_keep_versions),keep_versions);gchar *text=g_strdup_printf("Backups\n%s",backup);gtk_label_set_text(GTK_LABEL(settings_locations),text);g_free(text);}
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

void holder_settings_folders(char *folders){gtk_text_buffer_set_text(gtk_text_view_get_buffer(GTK_TEXT_VIEW(settings_folders)),folders,-1);}
