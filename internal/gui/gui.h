#include <gtk/gtk.h>
int holder_init(void);
void holder_run(void);
void holder_present(void);
void holder_begin_refresh(void);
void holder_add(char *id, char *name, char *version, char *path, char *icon, char *update, char *fingerprint, char *progress_text, int percent, int active, int can_update, int can_rollback, int saved, int pinned, int auto_update, int can_auto_update, int sandboxed, int sandbox_network);
void holder_status(char *text);

void holder_open(char *path);

void holder_tray_mode(void);

void holder_versions(char *id, char *text);

void holder_startup_state(int enabled);

void holder_finish(int count, int updates, int offered, int active);
void holder_settings(char *folders, char *backup, int keep_versions);
void holder_settings_keep(int keep_versions);
void holder_settings_status(char *text);
void holder_wake(void);

void holder_details(char *name, char *text);

void holder_version(char *text);
void holder_update_available(int available);

void holder_settings_folders(char *folders);
void holder_theme(char *theme);
void holder_app_defaults(int automatic, int sandbox, int network);
