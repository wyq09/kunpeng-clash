import React, { createContext, useCallback, useContext, useRef, useState } from "react";

/** 轻量 toast：showToast(message, type)，2.6s 自动消失。 */

type ToastType = "info" | "success" | "error";

interface ToastItem {
  id: number;
  message: string;
  type: ToastType;
}

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
    setItems((prev) => [...prev.slice(-3), { id, message, type }]);
    const t = setTimeout(() => {
      setItems((prev) => prev.filter((x) => x.id !== id));
      timers.current.delete(t);
    }, 2600);
    timers.current.add(t);
  }, []);

  return (
    <ToastContext.Provider value={{ showToast }}>
      {children}
      <div className="toast-wrap">
        {items.map((t) => (
          <div key={t.id} className={`toast ${t.type}`}>
            {t.message}
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}
