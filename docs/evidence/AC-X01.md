# AC-X01: registry 示例

- **切片**: S-01.
- **测试**: examples/registry
- **命令**: `go run ./examples/registry`
- **原始输出**:

```text
ext_data_control_manager_v1              v1   name=36
ext_foreign_toplevel_image_capture_source_manager_v1 v1   name=42
ext_foreign_toplevel_list_v1             v1   name=25
ext_idle_notifier_v1                     v2   name=10
ext_image_copy_capture_manager_v1        v1   name=33
ext_output_image_capture_source_manager_v1 v1   name=34
ext_session_lock_manager_v1              v1   name=27
ext_transient_seat_manager_v1            v1   name=57
ext_workspace_manager_v1                 v1   name=28
org_kde_kwin_server_decoration_manager   v1   name=15
wl_compositor                            v6   name=5
wl_data_device_manager                   v3   name=7
wl_fixes                                 v1   name=1
wl_output                                v4   name=83
wl_output                                v4   name=84
wl_seat                                  v9   name=58
wl_shm                                   v2   name=2
wl_subcompositor                         v1   name=6
wp_alpha_modifier_v1                     v1   name=20
wp_color_manager_v1                      v2   name=51
wp_color_representation_manager_v1       v1   name=52
wp_content_type_manager_v1               v1   name=40
wp_cursor_shape_manager_v1               v2   name=50
wp_drm_lease_device_v1                   v1   name=29
wp_drm_lease_device_v1                   v1   name=30
wp_fractional_scale_manager_v1           v1   name=41
wp_linux_drm_syncobj_manager_v1          v1   name=4
wp_presentation                          v2   name=19
wp_security_context_manager_v1           v1   name=37
wp_single_pixel_buffer_manager_v1        v1   name=39
wp_tearing_control_manager_v1            v1   name=43
wp_viewporter                            v1   name=38
xdg_activation_v1                        v1   name=48
xdg_toplevel_tag_manager_v1              v1   name=49
xdg_wm_base                              v5   name=13
zwlr_data_control_manager_v1             v2   name=35
zwlr_export_dmabuf_manager_v1            v1   name=31
zwlr_foreign_toplevel_manager_v1         v3   name=26
zwlr_gamma_control_manager_v1            v1   name=8
zwlr_layer_shell_v1                      v5   name=12
zwlr_output_manager_v1                   v4   name=21
zwlr_output_power_manager_v1             v1   name=22
zwlr_screencopy_manager_v1               v3   name=32
zwlr_virtual_pointer_manager_v1          v2   name=54
zwp_idle_inhibit_manager_v1              v1   name=11
zwp_input_method_manager_v2              v1   name=23
zwp_keyboard_shortcuts_inhibit_manager_v1 v1   name=55
zwp_linux_dmabuf_v1                      v5   name=3
zwp_pointer_constraints_v1               v1   name=18
zwp_pointer_gestures_v1                  v3   name=56
zwp_primary_selection_device_manager_v1  v1   name=60
zwp_relative_pointer_manager_v1          v1   name=17
zwp_tablet_manager_v2                    v1   name=14
zwp_text_input_manager_v3                v1   name=24
zwp_virtual_keyboard_manager_v1          v1   name=53
zxdg_decoration_manager_v1               v1   name=16
zxdg_exporter_v1                         v1   name=44
zxdg_exporter_v2                         v1   name=46
zxdg_importer_v1                         v1   name=45
zxdg_importer_v2                         v1   name=47
zxdg_output_manager_v1                   v3   name=9
```

- **结论**: 退出码 0; 输出包含 wl_compositor v6, wl_shm v2, xdg_wm_base v5.
