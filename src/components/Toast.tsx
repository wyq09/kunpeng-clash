import React, { createContext, useCallback, useContext, useRef, useState } from "react";

/** 轻量 toast：showToast(message, type)，2.6s 自动消失。 */

type ToastType = "info" | "success" | "error";

interface ToastItem {
  id: number;
  message: string;
  type: ToastType;
  leaving?: boolean;
}

const LEAVE_MS = 220;

interface ToastContextValue {
  showToast: (message: string, type?: ToastType) => void;
}

const ToastContext = createContext<ToastContextValue>({ showToast: () => {} });

export function useToast(): ToastContextValue {
  return useContext(ToastContext);
}

let nextId = 1;

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const timers = useRef(new Set<ReturnType<typeof setTimeout>>());

  const showToast = useCallback((message: string, type: ToastType = "info") => {
    const id = nextId++;
    setItems((prev) => {
      // 同一条消息正在显示时不重复堆叠
      if (prev.some((x) => x.message === message && !x.leaving)) return prev;
      return [...prev.slice(-3), { id, message, type }];
    });
    const later = (fn: () => void, ms: number) => {
      const t = setTimeout(() => {
        timers.current.delete(t);
        fn();
      }, ms);
      timers.current.add(t);
    };
    later(() => {
      setItems((prev) => prev.map((x) => (x.id === id ? { ...x, leaving: true } : x)));
      later(() => setItems((prev) => prev.filter((x) => x.id !== id)), LEAVE_MS);
    }, 2600);
  }, []);

  return (
    <ToastContext.Provider value={{ showToast }}>
      {children}
      <div className="toast-wrap" role="status" aria-live="polite">
        {items.map((t) => (
          <div key={t.id} className={`toast ${t.type}${t.leaving ? " leaving" : ""}`}>
            <span className={`toast-dot ${t.type}`} />
            {t.message}
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}
