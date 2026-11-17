import React, { useEffect, useRef } from 'react';
import { X } from 'lucide-react';

interface DialogProps {
  title: string;
  onClose: () => void;
  children: React.ReactNode;
}

/** Confirmation dialog: modal, focus moves in on open, Escape and backdrop close it. */
export const Dialog: React.FC<DialogProps> = ({ title, onClose, children }) => {
  const panelRef = useRef<HTMLDivElement>(null);
  const titleId = useRef(`dialog-${Math.random().toString(36).slice(2)}`).current;
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  // Runs once per open: focus the first field, close on Escape, restore focus after.
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    panelRef.current?.querySelector<HTMLElement>('input, button:not([data-close])')?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onCloseRef.current();
    };
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('keydown', onKey);
      previous?.focus();
    };
  }, []);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-[#181717]/25 p-4" onClick={onClose}>
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onClick={(e) => e.stopPropagation()}
        className="w-full max-w-md panel p-6 shadow-xl"
      >
        <div className="flex items-start justify-between gap-4 mb-3">
          <h2 id={titleId} className="text-lg font-medium text-slate-100">{title}</h2>
          <button data-close onClick={onClose} aria-label="Close" className="btn btn-ghost btn-sm -mr-2 -mt-1">
            <X />
          </button>
        </div>
        {children}
      </div>
    </div>
  );
};
