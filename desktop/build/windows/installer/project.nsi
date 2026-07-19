Unicode true

# Wails resolves project metadata and architecture helpers into this generated
# include before invoking makensis. Keep this reviewed project file in source so
# association quoting and Authenticode hooks cannot drift with CLI defaults.
!include "wails_tools.nsh"

VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion "${INFO_PRODUCTVERSION}.0"
VIAddVersionKey "CompanyName" "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion" "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion" "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright" "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName" "${INFO_PRODUCTNAME}"

ManifestDPIAware true
!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_FINISHPAGE_NOAUTOCLOSE
!define MUI_ABORTWARNING

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

# WriteUninstaller creates a separate PE file while makensis runs. Sign that
# file before it is embedded so enterprise application-control also trusts the
# installed uninstaller. The wrapper is a no-op in credential-free PR builds.
!uninstfinalize 'bash "..\..\..\..\scripts\sign-windows-binary.sh" "%1"'

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe"
!ifdef WAILS_INSTALL_SCOPE
  !if "${WAILS_INSTALL_SCOPE}" == "user"
    InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
  !else
    InstallDir "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
  !endif
!else
  InstallDir "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
!endif
ShowInstDetails show

# Wails' stock association macro leaves the executable unquoted and replaces
# its backup on a reinstall. Register the namespaced ProgID directly so we can
# preserve the pre-install default once and restore it only while APEX Load is
# still the selected handler.
!define APEXLOAD_FILE_CHAIN_KEY "Software\Classes\APEXLoad.HTTPArchive\APEXLoadOwnershipChain"

!macro apexload.associateFiles
  StrCpy $8 "$INSTDIR\${PRODUCT_EXECUTABLE}"
  StrCpy $9 "$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\" $\"%1$\""
  ReadRegStr $2 SHCTX "Software\Classes\APEXLoad.HTTPArchive\shell\open\command" ""
  ReadRegStr $3 SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadOwnedCommand"
  StrCmp $2 "" apexload_prepare_file_registration
  StrCmp $2 $9 apexload_migrate_legacy_file_owner
  # A prior APEX install can be refreshed to a new directory only while the
  # command still equals the ownership marker that install wrote. If its shell
  # command was edited later, preserve the complete ProgID byte-for-byte.
  StrCmp $3 "" apexload_preserve_file_registration
  StrCmp $2 $3 0 apexload_preserve_file_registration
  ReadRegStr $4 SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadOwnedExecutable"
  StrCmp $4 "" apexload_preserve_file_registration
  # Upgrade the old single-predecessor format into a per-install chain before
  # linking this new install. Registry value names may contain the executable
  # path verbatim, so every installed copy keeps its own predecessor edge.
  ReadRegStr $5 SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadPreviousOwnedExecutable"
  StrCmp $5 "" apexload_link_file_owner
  WriteRegStr SHCTX "${APEXLOAD_FILE_CHAIN_KEY}" "$4" "$5"

apexload_link_file_owner:
  WriteRegStr SHCTX "${APEXLOAD_FILE_CHAIN_KEY}" "$8" "$4"
  DeleteRegValue SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadPreviousOwnedCommand"
  DeleteRegValue SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadPreviousOwnedExecutable"
  Goto apexload_prepare_file_registration

apexload_migrate_legacy_file_owner:
  ReadRegStr $4 SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadPreviousOwnedExecutable"
  StrCmp $4 "" apexload_prepare_file_registration
  WriteRegStr SHCTX "${APEXLOAD_FILE_CHAIN_KEY}" "$8" "$4"
  DeleteRegValue SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadPreviousOwnedCommand"
  DeleteRegValue SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadPreviousOwnedExecutable"

apexload_prepare_file_registration:
  ReadRegStr $0 SHCTX "Software\Classes\.har" ""
  StrCmp $0 "APEXLoad.HTTPArchive" apexload_write_default_association
  ReadRegStr $1 SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadBackupCaptured"
  # Once the original default has been captured, a different current handler
  # is a user choice made after installation. Repair/upgrade the APEX ProgID,
  # but never reclaim .har from that newer choice.
  StrCmp $1 "1" apexload_refresh_registration
  WriteRegStr SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadBackupProgID" "$0"
  WriteRegStr SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadBackupCaptured" "1"

apexload_write_default_association:
  WriteRegStr SHCTX "Software\Classes\.har" "" "APEXLoad.HTTPArchive"

apexload_refresh_registration:
  File "..\appicon.ico"
  WriteRegStr SHCTX "Software\Classes\.har\OpenWithProgids" "APEXLoad.HTTPArchive" ""
  WriteRegStr SHCTX "Software\Classes\APEXLoad.HTTPArchive" "" "HTTP Archive"
  WriteRegStr SHCTX "Software\Classes\APEXLoad.HTTPArchive\DefaultIcon" "" "$INSTDIR\appicon.ico"
  WriteRegStr SHCTX "Software\Classes\APEXLoad.HTTPArchive\shell" "" "open"
  WriteRegStr SHCTX "Software\Classes\APEXLoad.HTTPArchive\shell\open" "" "Open with ${INFO_PRODUCTNAME}"
  WriteRegStr SHCTX "Software\Classes\APEXLoad.HTTPArchive\shell\open\command" "" "$9"
  WriteRegStr SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadOwnedCommand" "$9"
  WriteRegStr SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadOwnedExecutable" "$INSTDIR\${PRODUCT_EXECUTABLE}"
  System::Call 'shell32::SHChangeNotify(i 0x08000000, i 0, i 0, i 0)'

apexload_preserve_file_registration:
!macroend

!macro apexload.unassociateFiles
  StrCpy $8 "$INSTDIR\${PRODUCT_EXECUTABLE}"
  StrCpy $9 "$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\" $\"%1$\""
  ReadRegStr $2 SHCTX "Software\Classes\APEXLoad.HTTPArchive\shell\open\command" ""
  ReadRegStr $3 SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadOwnedCommand"
  StrCmp $2 $9 0 apexload_preserve_user_association
  StrCmp $3 $9 0 apexload_preserve_user_association

  # Follow the per-install predecessor chain. Missing executables are skipped so
  # uninstalling B before C in A -> B -> C still lets C restore A later.
  ReadRegStr $4 SHCTX "${APEXLOAD_FILE_CHAIN_KEY}" "$8"
  DeleteRegValue SHCTX "${APEXLOAD_FILE_CHAIN_KEY}" "$8"
  StrCpy $6 0
apexload_find_previous_file_owner:
  StrCmp $4 "" apexload_restore_original_file_association
  IntCmpU $6 64 apexload_restore_original_file_association apexload_check_previous_file_owner apexload_restore_original_file_association
apexload_check_previous_file_owner:
  IfFileExists "$4" apexload_restore_previous_file_owner apexload_skip_missing_file_owner
apexload_skip_missing_file_owner:
  ReadRegStr $5 SHCTX "${APEXLOAD_FILE_CHAIN_KEY}" "$4"
  DeleteRegValue SHCTX "${APEXLOAD_FILE_CHAIN_KEY}" "$4"
  StrCpy $4 "$5"
  IntOp $6 $6 + 1
  Goto apexload_find_previous_file_owner

apexload_restore_previous_file_owner:
  StrCpy $5 "$\"$4$\" $\"%1$\""
  WriteRegStr SHCTX "Software\Classes\APEXLoad.HTTPArchive\DefaultIcon" "" "$\"$4$\",0"
  WriteRegStr SHCTX "Software\Classes\APEXLoad.HTTPArchive\shell\open\command" "" "$5"
  WriteRegStr SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadOwnedCommand" "$5"
  WriteRegStr SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadOwnedExecutable" "$4"
  DeleteRegValue SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadPreviousOwnedCommand"
  DeleteRegValue SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadPreviousOwnedExecutable"
  DeleteRegKey /ifempty SHCTX "${APEXLOAD_FILE_CHAIN_KEY}"
  Goto apexload_finish_file_unregistration

apexload_restore_original_file_association:
  ReadRegStr $0 SHCTX "Software\Classes\.har" ""
  StrCmp $0 "APEXLoad.HTTPArchive" 0 apexload_preserve_user_association
  ReadRegStr $1 SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadBackupCaptured"
  StrCmp $1 "1" 0 apexload_delete_current_association
  ReadRegStr $1 SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadBackupProgID"
  StrCmp $1 "" apexload_delete_current_association
  WriteRegStr SHCTX "Software\Classes\.har" "" "$1"
  Goto apexload_preserve_user_association

apexload_delete_current_association:
  DeleteRegValue SHCTX "Software\Classes\.har" ""

apexload_preserve_user_association:
  # Delete shared registration only if this uninstall still owns it. Unknown
  # verbs/values are retained by removing known data and pruning empty keys.
  StrCmp $2 $9 0 apexload_finish_file_unregistration
  StrCmp $3 $9 0 apexload_finish_file_unregistration
  DeleteRegValue SHCTX "Software\Classes\.har\OpenWithProgids" "APEXLoad.HTTPArchive"
  DeleteRegKey SHCTX "Software\Classes\APEXLoad.HTTPArchive\DefaultIcon"
  DeleteRegKey SHCTX "Software\Classes\APEXLoad.HTTPArchive\shell\open\command"
  DeleteRegKey /ifempty SHCTX "Software\Classes\APEXLoad.HTTPArchive\shell\open"
  DeleteRegKey /ifempty SHCTX "Software\Classes\APEXLoad.HTTPArchive\shell"
  DeleteRegValue SHCTX "Software\Classes\APEXLoad.HTTPArchive" ""
  DeleteRegValue SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadOwnedCommand"
  DeleteRegValue SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadOwnedExecutable"
  DeleteRegValue SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadPreviousOwnedCommand"
  DeleteRegValue SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadPreviousOwnedExecutable"
  DeleteRegValue SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadBackupProgID"
  DeleteRegValue SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadBackupCaptured"
  DeleteRegKey /ifempty SHCTX "${APEXLOAD_FILE_CHAIN_KEY}"
  DeleteRegKey /ifempty SHCTX "Software\Classes\APEXLoad.HTTPArchive"

apexload_finish_file_unregistration:
  System::Call 'shell32::SHChangeNotify(i 0x08000000, i 0, i 0, i 0)'
!macroend

# The stock Wails custom-protocol macros delete the complete scheme key on both
# install and uninstall. A protocol key can contain arbitrary owner-specific
# verbs, DDE/DelegateExecute data, and capabilities that cannot be faithfully
# reconstructed from a few scalar values. Never claim a non-empty key owned by
# another command; delete only while the command still points at this install.
!define APEXLOAD_PROTOCOL_KEY "Software\Classes\apexload"
!define APEXLOAD_PROTOCOL_OWNER "APEXLoadOwner"
!define APEXLOAD_PROTOCOL_OWNED_COMMAND "APEXLoadOwnedCommand"
!define APEXLOAD_PROTOCOL_OWNED_EXECUTABLE "APEXLoadOwnedExecutable"
!define APEXLOAD_PROTOCOL_PREVIOUS_COMMAND "APEXLoadPreviousOwnedCommand"
!define APEXLOAD_PROTOCOL_PREVIOUS_EXECUTABLE "APEXLoadPreviousOwnedExecutable"
!define APEXLOAD_PROTOCOL_CHAIN_KEY "${APEXLOAD_PROTOCOL_KEY}\APEXLoadOwnershipChain"
!define APEXLOAD_INSTALL_METADATA_KEY "Software\APEXLoad\DesktopInstaller"
!define APEXLOAD_INSTALL_CURRENT_EXECUTABLE "CurrentExecutable"
!define APEXLOAD_INSTALL_SHORTCUT_OWNER "ShortcutOwner"

!macro apexload.associateCustomProtocols
  StrCpy $8 "$INSTDIR\${PRODUCT_EXECUTABLE}"
  StrCpy $9 "$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\" $\"%1$\""
  ReadRegStr $0 SHCTX "${APEXLOAD_PROTOCOL_KEY}\shell\open\command" ""
  StrCmp $0 $9 apexload_protocol_migrate_legacy_owner
  StrCmp $0 "" apexload_protocol_detect_existing
  ReadRegStr $1 SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_OWNER}"
  StrCmp $1 "1" 0 apexload_protocol_preserve_existing
  ReadRegStr $2 SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_OWNED_COMMAND}"
  StrCmp $0 $2 0 apexload_protocol_preserve_existing
  ReadRegStr $3 SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_OWNED_EXECUTABLE}"
  StrCmp $3 "" apexload_protocol_preserve_existing
  ReadRegStr $4 SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_PREVIOUS_EXECUTABLE}"
  StrCmp $4 "" apexload_protocol_link_owner
  WriteRegStr SHCTX "${APEXLOAD_PROTOCOL_CHAIN_KEY}" "$3" "$4"

apexload_protocol_link_owner:
  WriteRegStr SHCTX "${APEXLOAD_PROTOCOL_CHAIN_KEY}" "$8" "$3"
  DeleteRegValue SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_PREVIOUS_COMMAND}"
  DeleteRegValue SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_PREVIOUS_EXECUTABLE}"
  Goto apexload_protocol_register

apexload_protocol_migrate_legacy_owner:
  ReadRegStr $3 SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_PREVIOUS_EXECUTABLE}"
  StrCmp $3 "" apexload_protocol_register
  WriteRegStr SHCTX "${APEXLOAD_PROTOCOL_CHAIN_KEY}" "$8" "$3"
  DeleteRegValue SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_PREVIOUS_COMMAND}"
  DeleteRegValue SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_PREVIOUS_EXECUTABLE}"
  Goto apexload_protocol_register

apexload_protocol_detect_existing:
  # ReadRegStr cannot distinguish a missing key from a missing value. Check its
  # default value, first subkey, and first named value; a truly empty placeholder
  # is safe to populate, while any owner data is preserved byte-for-byte.
  ClearErrors
  ReadRegStr $1 SHCTX "${APEXLOAD_PROTOCOL_KEY}" ""
  IfErrors apexload_protocol_check_subkey apexload_protocol_preserve_existing
apexload_protocol_check_subkey:
  ClearErrors
  EnumRegKey $1 SHCTX "${APEXLOAD_PROTOCOL_KEY}" 0
  IfErrors apexload_protocol_check_value apexload_protocol_preserve_existing
apexload_protocol_check_value:
  ClearErrors
  EnumRegValue $1 SHCTX "${APEXLOAD_PROTOCOL_KEY}" 0
  IfErrors apexload_protocol_register apexload_protocol_preserve_existing

apexload_protocol_register:
  WriteRegStr SHCTX "${APEXLOAD_PROTOCOL_KEY}" "" "APEX Load deep link"
  WriteRegStr SHCTX "${APEXLOAD_PROTOCOL_KEY}" "URL Protocol" ""
  WriteRegStr SHCTX "${APEXLOAD_PROTOCOL_KEY}\DefaultIcon" "" "$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\",0"
  WriteRegStr SHCTX "${APEXLOAD_PROTOCOL_KEY}\shell" "" "open"
  WriteRegStr SHCTX "${APEXLOAD_PROTOCOL_KEY}\shell\open\command" "" "$9"
  WriteRegStr SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_OWNER}" "1"
  WriteRegStr SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_OWNED_COMMAND}" "$9"
  WriteRegStr SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_OWNED_EXECUTABLE}" "$INSTDIR\${PRODUCT_EXECUTABLE}"

apexload_protocol_preserve_existing:
!macroend

# Inspect every historic uninstall scope/view before making any installation
# mutation. A live registration at the selected canonical executable is a
# repair/upgrade; another live path must be uninstalled first. Product-owned
# registrations whose executable disappeared are cleaned only in the active
# installer scope; stale foreign-scope ownership is a blocking conflict because
# an unelevated installer cannot verify that registry cleanup succeeded.
!macro apexload.inspectExistingInstall ROOT FLAG SUFFIX ALLOW_SAME_PATH CHECK_ASSOCIATIONS
  StrCpy ${FLAG} "0"
  StrCpy $5 "0"
  StrCpy $6 "0"

  ReadRegStr $0 ${ROOT} "${APEXLOAD_INSTALL_METADATA_KEY}" "${APEXLOAD_INSTALL_CURRENT_EXECUTABLE}"
  StrCmp $0 "" apexload_${SUFFIX}_check_location
  StrCpy $5 "1"
  GetFullPathName $1 "$0"
  StrCmp $1 $9 apexload_${SUFFIX}_same_metadata
  IfFileExists "$1" apexload_${SUFFIX}_conflict apexload_${SUFFIX}_check_location
apexload_${SUFFIX}_same_metadata:
  !if "${ALLOW_SAME_PATH}" == "1"
    StrCpy $6 "1"
    Goto apexload_${SUFFIX}_check_location
  !else
    IfFileExists "$1" 0 apexload_${SUFFIX}_check_location
    StrCpy $8 "$1"
    Goto apexload_install_location_conflict
  !endif

apexload_${SUFFIX}_check_location:
  ReadRegStr $0 ${ROOT} "${UNINST_KEY}" "InstallLocation"
  StrCmp $0 "" apexload_${SUFFIX}_check_icon
  StrCpy $5 "1"
  GetFullPathName $1 "$0\${PRODUCT_EXECUTABLE}"
  StrCmp $1 $9 apexload_${SUFFIX}_same_location
  IfFileExists "$1" apexload_${SUFFIX}_conflict apexload_${SUFFIX}_check_icon
apexload_${SUFFIX}_same_location:
  !if "${ALLOW_SAME_PATH}" == "1"
    StrCpy $6 "1"
    Goto apexload_${SUFFIX}_check_icon
  !else
    IfFileExists "$1" 0 apexload_${SUFFIX}_check_icon
    StrCpy $8 "$1"
    Goto apexload_install_location_conflict
  !endif

apexload_${SUFFIX}_check_icon:
  ReadRegStr $0 ${ROOT} "${UNINST_KEY}" "DisplayIcon"
  !if "${CHECK_ASSOCIATIONS}" == "1"
    StrCmp $0 "" apexload_${SUFFIX}_check_file_owner
  !else
    StrCmp $0 "" apexload_${SUFFIX}_check_display_name
  !endif
  StrCpy $5 "1"
  GetFullPathName $1 "$0"
  StrCmp $1 $9 apexload_${SUFFIX}_same_icon
  !if "${CHECK_ASSOCIATIONS}" == "1"
    IfFileExists "$1" apexload_${SUFFIX}_conflict apexload_${SUFFIX}_check_file_owner
  !else
    IfFileExists "$1" apexload_${SUFFIX}_conflict apexload_${SUFFIX}_check_display_name
  !endif
apexload_${SUFFIX}_same_icon:
  !if "${ALLOW_SAME_PATH}" == "1"
    StrCpy $6 "1"
    !if "${CHECK_ASSOCIATIONS}" == "1"
      Goto apexload_${SUFFIX}_check_file_owner
    !else
      Goto apexload_${SUFFIX}_check_display_name
    !endif
  !else
    !if "${CHECK_ASSOCIATIONS}" == "1"
      IfFileExists "$1" 0 apexload_${SUFFIX}_check_file_owner
    !else
      IfFileExists "$1" 0 apexload_${SUFFIX}_check_display_name
    !endif
    StrCpy $8 "$1"
    Goto apexload_install_location_conflict
  !endif

!if "${CHECK_ASSOCIATIONS}" == "1"
apexload_${SUFFIX}_check_file_owner:
  ReadRegStr $0 ${ROOT} "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadOwnedExecutable"
  StrCmp $0 "" apexload_${SUFFIX}_check_protocol_owner
  StrCpy $5 "1"
  GetFullPathName $1 "$0"
  StrCmp $1 $9 apexload_${SUFFIX}_same_file_owner
  IfFileExists "$1" apexload_${SUFFIX}_conflict apexload_${SUFFIX}_check_protocol_owner
apexload_${SUFFIX}_same_file_owner:
  !if "${ALLOW_SAME_PATH}" == "1"
    StrCpy $6 "1"
    Goto apexload_${SUFFIX}_check_protocol_owner
  !else
    IfFileExists "$1" 0 apexload_${SUFFIX}_check_protocol_owner
    StrCpy $8 "$1"
    Goto apexload_install_location_conflict
  !endif

apexload_${SUFFIX}_check_protocol_owner:
  ReadRegStr $0 ${ROOT} "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_OWNED_EXECUTABLE}"
  StrCmp $0 "" apexload_${SUFFIX}_check_display_name
  StrCpy $5 "1"
  GetFullPathName $1 "$0"
  StrCmp $1 $9 apexload_${SUFFIX}_same_protocol_owner
  IfFileExists "$1" apexload_${SUFFIX}_conflict apexload_${SUFFIX}_check_display_name
apexload_${SUFFIX}_same_protocol_owner:
  !if "${ALLOW_SAME_PATH}" == "1"
    StrCpy $6 "1"
    Goto apexload_${SUFFIX}_check_display_name
  !else
    IfFileExists "$1" 0 apexload_${SUFFIX}_check_display_name
    StrCpy $8 "$1"
    Goto apexload_install_location_conflict
  !endif
!endif

apexload_${SUFFIX}_check_display_name:
  ReadRegStr $0 ${ROOT} "${UNINST_KEY}" "DisplayName"
  StrCmp $0 "" apexload_${SUFFIX}_finish
  StrCpy $5 "1"
apexload_${SUFFIX}_finish:
  # A record outside the installer-owned scope is a conflict even when its
  # executable has disappeared. A per-user installer cannot reliably delete
  # HKLM state, and copying into the referenced path would make that foreign
  # ownership live again. Require explicit cleanup in the owning scope.
  !if "${ALLOW_SAME_PATH}" == "0"
    StrCmp $5 "1" 0 apexload_${SUFFIX}_done
    StrCpy $8 "a stale ownership record in a different Windows registry scope"
    Goto apexload_install_location_conflict
  !endif
  StrCmp $6 "1" apexload_${SUFFIX}_done
  StrCmp $5 "1" 0 apexload_${SUFFIX}_done
  StrCpy ${FLAG} "1"
  Goto apexload_${SUFFIX}_done
apexload_${SUFFIX}_conflict:
  StrCpy $8 "$1"
  Goto apexload_install_location_conflict
apexload_${SUFFIX}_done:
!macroend

!macro apexload.deleteStaleInstallRegistration ROOT FLAG SUFFIX
  StrCmp ${FLAG} "1" 0 apexload_${SUFFIX}_stale_done
  DeleteRegKey ${ROOT} "${APEXLOAD_INSTALL_METADATA_KEY}"
  DeleteRegKey ${ROOT} "${UNINST_KEY}"
apexload_${SUFFIX}_stale_done:
!macroend

Function apexload.ensureSingleInstallLocation
  GetFullPathName $9 "$INSTDIR\${PRODUCT_EXECUTABLE}"
  SetRegView 64
  !ifdef WAILS_INSTALL_SCOPE
    !if "${WAILS_INSTALL_SCOPE}" == "user"
      # HKCU\Software is shared between registry views, so this is the one
      # owning per-user registration. HKLM is a foreign install scope even
      # when it identifies the selected physical path.
      !insertmacro apexload.inspectExistingInstall HKCU $R0 user64 1 1
      !insertmacro apexload.inspectExistingInstall HKLM $R1 machine64 0 1
    !else
      !insertmacro apexload.inspectExistingInstall HKLM $R0 machine64 1 1
      !insertmacro apexload.inspectExistingInstall HKCU $R1 user64 0 1
    !endif
  !else
    !insertmacro apexload.inspectExistingInstall HKLM $R0 machine64 1 1
    !insertmacro apexload.inspectExistingInstall HKCU $R1 user64 0 1
  !endif
  # HKLM\Software is redirected. Check the non-owning 32-bit ARP and product
  # metadata view, but do not rescan HKLM\Software\Classes: those association
  # keys are shared on supported Windows versions and were checked above.
  SetRegView 32
  !insertmacro apexload.inspectExistingInstall HKLM $R2 machine32 0 0

  # No conflicting live or foreign-scope record was found. Remove stale state
  # only from the installer-owned scope, where permissions and ownership are
  # both deterministic, then restore the 64-bit view used by Wails.
  SetRegView 64
  !ifdef WAILS_INSTALL_SCOPE
    !if "${WAILS_INSTALL_SCOPE}" == "user"
      !insertmacro apexload.deleteStaleInstallRegistration HKCU $R0 user64_cleanup
    !else
      !insertmacro apexload.deleteStaleInstallRegistration HKLM $R0 machine64_cleanup
    !endif
  !else
    !insertmacro apexload.deleteStaleInstallRegistration HKLM $R0 machine64_cleanup
  !endif
  SetRegView 64
  Return

apexload_install_location_conflict:
  SetRegView 64
  SetErrorLevel 68
  IfSilent apexload_install_location_abort apexload_install_location_show_error
apexload_install_location_show_error:
  MessageBox MB_OK|MB_ICONSTOP "Another APEX Load desktop installation or ownership record is present at:$\r$\n$8$\r$\n$\r$\nUninstall it before continuing. If its files were already removed, clear the stale entry from its original user/machine scope first. Same-folder repair remains supported only in the current installer scope."
apexload_install_location_abort:
  Quit
FunctionEnd

!macro apexload.writeInstallerOwnership
  SetRegView 64
  WriteRegStr SHCTX "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr SHCTX "${APEXLOAD_INSTALL_METADATA_KEY}" "${APEXLOAD_INSTALL_CURRENT_EXECUTABLE}" "$INSTDIR\${PRODUCT_EXECUTABLE}"
  WriteRegStr SHCTX "${APEXLOAD_INSTALL_METADATA_KEY}" "${APEXLOAD_INSTALL_SHORTCUT_OWNER}" "$INSTDIR\${PRODUCT_EXECUTABLE}"
!macroend

# Shared WebView state, fixed-name shortcuts, and the fixed Add/Remove Programs
# key are removed only while the explicit ownership markers still identify this
# installation. The uninstaller executable itself is installation-local and is
# always removed.
!macro apexload.removeOwnedInstallerState
  SetRegView 64
  StrCpy $8 "$INSTDIR\${PRODUCT_EXECUTABLE}"
  ReadRegStr $0 SHCTX "${APEXLOAD_INSTALL_METADATA_KEY}" "${APEXLOAD_INSTALL_CURRENT_EXECUTABLE}"
  StrCmp $0 $8 0 apexload_shared_install_state_done

  ReadRegStr $1 SHCTX "${APEXLOAD_INSTALL_METADATA_KEY}" "${APEXLOAD_INSTALL_SHORTCUT_OWNER}"
  StrCmp $1 $8 0 apexload_preserve_shared_shortcuts
  Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
  Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"
apexload_preserve_shared_shortcuts:

  ReadRegStr $1 SHCTX "${UNINST_KEY}" "InstallLocation"
  ReadRegStr $2 SHCTX "${UNINST_KEY}" "UninstallString"
  StrCpy $3 "$\"$INSTDIR\uninstall.exe$\""
  StrCmp $1 "$INSTDIR" 0 apexload_preserve_shared_uninstall_key
  StrCmp $2 $3 0 apexload_preserve_shared_uninstall_key
  DeleteRegKey SHCTX "${UNINST_KEY}"
apexload_preserve_shared_uninstall_key:

  RMDir /r "$AppData\${PRODUCT_EXECUTABLE}"
  DeleteRegValue SHCTX "${APEXLOAD_INSTALL_METADATA_KEY}" "${APEXLOAD_INSTALL_CURRENT_EXECUTABLE}"
  DeleteRegValue SHCTX "${APEXLOAD_INSTALL_METADATA_KEY}" "${APEXLOAD_INSTALL_SHORTCUT_OWNER}"
  DeleteRegKey /ifempty SHCTX "${APEXLOAD_INSTALL_METADATA_KEY}"
  DeleteRegKey /ifempty SHCTX "Software\APEXLoad"

apexload_shared_install_state_done:
  Delete "$INSTDIR\uninstall.exe"
!macroend

!macro apexload.unassociateCustomProtocols
  StrCpy $8 "$INSTDIR\${PRODUCT_EXECUTABLE}"
  StrCpy $9 "$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\" $\"%1$\""
  ReadRegStr $0 SHCTX "${APEXLOAD_PROTOCOL_KEY}\shell\open\command" ""
  StrCmp $0 $9 0 apexload_protocol_preserve_current
  ReadRegStr $1 SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_OWNER}"
  StrCmp $1 "1" 0 apexload_protocol_preserve_current
  ReadRegStr $2 SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_OWNED_COMMAND}"
  StrCmp $2 $9 0 apexload_protocol_preserve_current

  ReadRegStr $3 SHCTX "${APEXLOAD_PROTOCOL_CHAIN_KEY}" "$8"
  DeleteRegValue SHCTX "${APEXLOAD_PROTOCOL_CHAIN_KEY}" "$8"
  StrCpy $6 0
apexload_protocol_find_previous_owner:
  StrCmp $3 "" apexload_protocol_remove_owned
  IntCmpU $6 64 apexload_protocol_remove_owned apexload_protocol_check_previous_owner apexload_protocol_remove_owned
apexload_protocol_check_previous_owner:
  IfFileExists "$3" apexload_protocol_restore_previous_owner apexload_protocol_skip_missing_owner
apexload_protocol_skip_missing_owner:
  ReadRegStr $4 SHCTX "${APEXLOAD_PROTOCOL_CHAIN_KEY}" "$3"
  DeleteRegValue SHCTX "${APEXLOAD_PROTOCOL_CHAIN_KEY}" "$3"
  StrCpy $3 "$4"
  IntOp $6 $6 + 1
  Goto apexload_protocol_find_previous_owner

apexload_protocol_restore_previous_owner:
  StrCpy $4 "$\"$3$\" $\"%1$\""
  WriteRegStr SHCTX "${APEXLOAD_PROTOCOL_KEY}\DefaultIcon" "" "$\"$3$\",0"
  WriteRegStr SHCTX "${APEXLOAD_PROTOCOL_KEY}\shell\open\command" "" "$4"
  WriteRegStr SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_OWNED_COMMAND}" "$4"
  WriteRegStr SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_OWNED_EXECUTABLE}" "$3"
  DeleteRegValue SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_PREVIOUS_COMMAND}"
  DeleteRegValue SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_PREVIOUS_EXECUTABLE}"
  DeleteRegKey /ifempty SHCTX "${APEXLOAD_PROTOCOL_CHAIN_KEY}"
  Goto apexload_protocol_preserve_current

apexload_protocol_remove_owned:
  DeleteRegKey SHCTX "${APEXLOAD_PROTOCOL_KEY}\DefaultIcon"
  DeleteRegKey SHCTX "${APEXLOAD_PROTOCOL_KEY}\shell\open\command"
  DeleteRegKey /ifempty SHCTX "${APEXLOAD_PROTOCOL_KEY}\shell\open"
  DeleteRegKey /ifempty SHCTX "${APEXLOAD_PROTOCOL_KEY}\shell"
  DeleteRegValue SHCTX "${APEXLOAD_PROTOCOL_KEY}" ""
  DeleteRegValue SHCTX "${APEXLOAD_PROTOCOL_KEY}" "URL Protocol"
  DeleteRegValue SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_OWNER}"
  DeleteRegValue SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_OWNED_COMMAND}"
  DeleteRegValue SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_OWNED_EXECUTABLE}"
  DeleteRegValue SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_PREVIOUS_COMMAND}"
  DeleteRegValue SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_PREVIOUS_EXECUTABLE}"
  DeleteRegKey /ifempty SHCTX "${APEXLOAD_PROTOCOL_CHAIN_KEY}"
  DeleteRegKey /ifempty SHCTX "${APEXLOAD_PROTOCOL_KEY}"

apexload_protocol_preserve_current:
  # Ownership changed after installation. Leave the current handler untouched.
!macroend

# Wails persists a per-user toast activator GUID and COM LocalServer32 entry at
# first launch. Remove only the activator whose command still points at this
# installation; a user or later install may have replaced either key.
!macro apexload.unassociateNotifications
  ReadRegStr $0 HKCU "Software\Classes\AppUserModelId\${PRODUCT_EXECUTABLE}" "CustomActivator"
  StrCmp $0 "" apexload_notifications_done
  StrLen $1 $0
  StrCmp $1 38 0 apexload_notifications_done
  # The GUID is also used in a temp filename. Reject path separators/ADS syntax
  # before using registry-controlled text as a path component.
  StrCpy $2 0
apexload_notifications_validate_guid:
  IntCmp $2 $1 apexload_notifications_guid_valid
  StrCpy $3 $0 1 $2
  StrCmp $3 "\" apexload_notifications_done
  StrCmp $3 "/" apexload_notifications_done
  StrCmp $3 ":" apexload_notifications_done
  IntOp $2 $2 + 1
  Goto apexload_notifications_validate_guid

apexload_notifications_guid_valid:
  StrCpy $9 "$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\" %1"
  ReadRegStr $4 HKCU "Software\Classes\CLSID\$0\LocalServer32" ""
  StrCmp $4 $9 0 apexload_notifications_done
  DeleteRegValue HKCU "Software\Classes\CLSID\$0\LocalServer32" ""
  DeleteRegKey /ifempty HKCU "Software\Classes\CLSID\$0\LocalServer32"
  DeleteRegKey /ifempty HKCU "Software\Classes\CLSID\$0"
  DeleteRegValue HKCU "Software\Classes\AppUserModelId\${PRODUCT_EXECUTABLE}" "CustomActivator"
  DeleteRegKey /ifempty HKCU "Software\Classes\AppUserModelId\${PRODUCT_EXECUTABLE}"
  DeleteRegKey HKCU "SOFTWARE\${PRODUCT_EXECUTABLE}\NotificationCategories"
  DeleteRegKey /ifempty HKCU "SOFTWARE\${PRODUCT_EXECUTABLE}"
  Delete "$TEMP\${PRODUCT_EXECUTABLE}$0.png"

apexload_notifications_done:
!macroend

Function .onInit
  !insertmacro wails.checkArchitecture
FunctionEnd

# Wails' stock macro launches the online WebView2 bootstrapper without
# observing its exit status or confirming that the runtime appeared. That can
# report a successful APEX Load install on an offline machine even though the
# application cannot start. Verify before and after the bootstrapper and fail
# the installer closed with an actionable error when the runtime is unavailable.
Function apexload.ensureWebView2Runtime
  SetRegView 64
  ReadRegStr $0 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  StrCmp $0 "" 0 apexload_webview2_ready
  ${If} ${REQUEST_EXECUTION_LEVEL} == "user"
    ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
    StrCmp $0 "" 0 apexload_webview2_ready
  ${EndIf}

  SetDetailsPrint both
  DetailPrint "Installing: Microsoft Edge WebView2 Runtime"
  SetDetailsPrint listonly
  InitPluginsDir
  CreateDirectory "$pluginsdir\webview2bootstrapper"
  SetOutPath "$pluginsdir\webview2bootstrapper"
  File "tmp\MicrosoftEdgeWebview2Setup.exe"
  ExecWait '"$pluginsdir\webview2bootstrapper\MicrosoftEdgeWebview2Setup.exe" /silent /install' $1

  # The bootstrapper can return environment-specific success codes, so the
  # registry is the authoritative result. Do not permit a missing runtime even
  # when the process happened to return zero.
  SetRegView 64
  ReadRegStr $0 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  StrCmp $0 "" 0 apexload_webview2_ready
  ${If} ${REQUEST_EXECUTION_LEVEL} == "user"
    ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
    StrCmp $0 "" 0 apexload_webview2_ready
  ${EndIf}

  SetDetailsPrint both
  DetailPrint "Microsoft Edge WebView2 Runtime installation failed (bootstrapper exit code $1)."
  IfSilent apexload_webview2_fail apexload_webview2_show_error
apexload_webview2_show_error:
  MessageBox MB_OK|MB_ICONSTOP "Microsoft Edge WebView2 Runtime could not be installed (exit code $1). Connect this computer to the internet or ask your administrator to install the Evergreen WebView2 Runtime from an approved offline package, then run APEX Load Setup again."
apexload_webview2_fail:
  SetErrorLevel 66
  Quit

apexload_webview2_ready:
  SetDetailsPrint both
  Return
FunctionEnd

Section
  !insertmacro wails.setShellContext
  # The single-location guard is deliberately the first mutating installer
  # operation. A rejected second location cannot partially change WebView,
  # application, registry, association, or shortcut state.
  Call apexload.ensureSingleInstallLocation
  Call apexload.ensureWebView2Runtime
  SetOutPath $INSTDIR
  !insertmacro wails.files

  CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
  CreateShortcut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

  !insertmacro apexload.associateFiles
  !insertmacro apexload.associateCustomProtocols
  !insertmacro wails.writeUninstaller
  !insertmacro apexload.writeInstallerOwnership
SectionEnd

Section "uninstall"
  !insertmacro wails.setShellContext
  # NSIS uninstallers default to the 32-bit registry view. The amd64 app's
  # notification activator CLSID is view-specific, so select the build's
  # 64-bit view before any ownership checks or cleanup.
  SetRegView 64
  # Windows locks a running executable. Refuse to unregister a still-running
  # installation and leave its uninstaller intact instead of creating an
  # orphaned, partially-uninstalled application.
  ClearErrors
  Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"
  IfFileExists "$INSTDIR\${PRODUCT_EXECUTABLE}" apexload_uninstall_running 0

  !insertmacro apexload.unassociateFiles
  !insertmacro apexload.unassociateCustomProtocols
  !insertmacro apexload.unassociateNotifications
  !insertmacro apexload.removeOwnedInstallerState
  Delete "$INSTDIR\appicon.ico"
  # Remove the install directory only when it is now empty. A custom/shared
  # directory's unrelated files are never recursively deleted.
  RMDir "$INSTDIR"
  Goto apexload_uninstall_done

apexload_uninstall_running:
  SetErrorLevel 67
  IfSilent apexload_uninstall_abort apexload_uninstall_show_running
apexload_uninstall_show_running:
  MessageBox MB_OK|MB_ICONSTOP "${INFO_PRODUCTNAME} is still running. Close it and run the uninstaller again."
apexload_uninstall_abort:
  Abort

apexload_uninstall_done:
SectionEnd
