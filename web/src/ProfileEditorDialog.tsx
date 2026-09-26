// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Scitrera LLC
// Copyright 2026 Fox Engine Ltd.
import { useEffect, useId, useRef, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";

export function ProfileEditorDialog({ children, name, formId, disabled, fallbackFocus, onCancel, onDelete, defaultProfile }: {
  children: ReactNode; name: string | null; formId: string; disabled: boolean;
  fallbackFocus: RefObject<HTMLButtonElement | null>; onCancel: () => void; onDelete?: () => void; defaultProfile: boolean;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const id = useId();
  useEffect(() => {
    const element = dialog.current!;
    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    element.showModal();
    heading.current?.focus();
    return () => {
      element.close();
      document.body.style.overflow = previousOverflow;
      if (previousFocus?.isConnected && !previousFocus.matches(":disabled")) previousFocus.focus();
      else fallbackFocus.current?.focus();
    };
  }, [fallbackFocus]);
  return createPortal(<dialog ref={dialog} className="profile-dialog" aria-labelledby={`${id}-title ${id}-name`} aria-describedby={`${id}-help`}
    onCancel={event => { event.preventDefault(); onCancel(); }}>
    <header className="profile-dialog-heading">
      <h2 ref={heading} tabIndex={-1} id={`${id}-title`}>{name === null ? "Add request profile" : "Edit profile"}</h2>
      <p id={`${id}-name`} className="profile-dialog-name">{name ?? ""}</p>
      <p id={`${id}-help`}>Changes stay in the configuration draft until you save it.</p>
    </header>
    <div className="profile-dialog-body">{children}</div>
    <footer className="profile-dialog-actions">
      {onDelete && <button type="button" className="danger-button" disabled={disabled} onClick={onDelete}
        aria-label={`${defaultProfile ? "Delete parameters for" : "Delete profile"} ${name}`}>{defaultProfile ? "Delete parameters" : "Delete profile"}</button>}
      <div className="profile-dialog-primary-actions">
        <button type="button" className="secondary-button" onClick={onCancel}>Cancel</button>
        <button type="submit" form={formId} className="primary-button" disabled={disabled}>{name === null ? "Add profile to draft" : "Apply to draft"}</button>
      </div>
    </footer>
  </dialog>, document.body);
}
