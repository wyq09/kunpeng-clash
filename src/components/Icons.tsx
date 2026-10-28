import React from "react";

/** 内联线性 SVG 图标（1.5px stroke，lucide 风格手写）。 */

interface IconProps {
  size?: number;
  className?: string;
  strokeWidth?: number;
  style?: React.CSSProperties;
}

function base(props: IconProps) {
  const { size = 20, strokeWidth = 1.5 } = props;
  return {
    width: size,
    height: size,
    viewBox: "0 0 24 24",
    fill: "none" as const,
    stroke: "currentColor",
    strokeWidth,
    strokeLinecap: "round" as const,
    strokeLinejoin: "round" as const,
    className: props.className,
    style: props.style,
  };
}

export const IconDashboard = (p: IconProps) => (
  <svg {...base(p)}>
    <rect x="3" y="3" width="7.5" height="7.5" rx="1.5" />
    <rect x="13.5" y="3" width="7.5" height="7.5" rx="1.5" />
    <rect x="3" y="13.5" width="7.5" height="7.5" rx="1.5" />
    <rect x="13.5" y="13.5" width="7.5" height="7.5" rx="1.5" />
  </svg>
);

export const IconGlobe = (p: IconProps) => (
  <svg {...base(p)}>
    <circle cx="12" cy="12" r="9" />
    <path d="M3 12h18" />
    <path d="M12 3a13.5 13.5 0 0 1 0 18a13.5 13.5 0 0 1 0-18z" />
  </svg>
);

export const IconFile = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z" />
    <path d="M14 3v5h5" />
    <path d="M9 13h6M9 17h4" />
  </svg>
);

export const IconConnections = (p: IconProps) => (
  <svg {...base(p)}>
    <circle cx="12" cy="5" r="2.4" />
    <circle cx="5" cy="19" r="2.4" />
    <circle cx="19" cy="19" r="2.4" />
    <path d="M10.8 6.9 6.4 16.8M13.2 6.9l4.4 9.9M7.4 19h9.2" />
  </svg>
);

export const IconTool = (p: IconProps) => (
  <svg {...base(p)}>
    <rect x="3" y="7" width="18" height="13" rx="2" />
    <path d="M9 7V5a2 2 0 0 1 2-2h2a2 2 0 0 1 2 2v2" />
    <path d="M3 12h18" />
    <path d="M12 12v2.5" />
  </svg>
);

export const IconSidebar = (p: IconProps) => (
  <svg {...base(p)}>
    <rect x="3" y="4" width="18" height="16" rx="2.5" />
    <path d="M9.5 4v16" />
  </svg>
);

export const IconSearch = (p: IconProps) => (
  <svg {...base(p)}>
    <circle cx="11" cy="11" r="7" />
    <path d="m20 20-3.5-3.5" />
  </svg>
);

export const IconRefresh = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M21 12a9 9 0 1 1-2.64-6.36" />
    <path d="M21 3v6h-6" />
  </svg>
);

export const IconPlus = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M12 5v14M5 12h14" />
  </svg>
);

export const IconPlusCircle = (p: IconProps) => (
  <svg {...base(p)}>
    <circle cx="12" cy="12" r="9" />
    <path d="M12 8v8M8 12h8" />
  </svg>
);

export const IconTrash = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M4 7h16" />
    <path d="M9 7V5a1.5 1.5 0 0 1 1.5-1.5h3A1.5 1.5 0 0 1 15 5v2" />
    <path d="M6.5 7l.8 12a2 2 0 0 0 2 1.9h5.4a2 2 0 0 0 2-1.9l.8-12" />
    <path d="M10 11v6M14 11v6" />
  </svg>
);

export const IconTrashCircle = (p: IconProps) => (
  <svg {...base(p)}>
    <circle cx="12" cy="12" r="9" />
    <path d="M9 10h6" />
    <path d="M10 10v5.5M14 10v5.5" />
    <path d="M10.5 7.5h3" />
  </svg>
);

export const IconPlay = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M8 5.5v13l11-6.5z" fill="currentColor" stroke="none" />
  </svg>
);

export const IconPause = (p: IconProps) => (
  <svg {...base(p)}>
    <rect x="7" y="5" width="3.4" height="14" rx="1" fill="currentColor" stroke="none" />
    <rect x="13.6" y="5" width="3.4" height="14" rx="1" fill="currentColor" stroke="none" />
  </svg>
);

export const IconZap = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M13 2 4.5 13.5H11L10 22l8.5-11.5H13z" />
  </svg>
);

export const IconCheck = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="m5 12.5 4.5 4.5L19 7.5" />
  </svg>
);

export const IconCheckCircle = (p: IconProps) => (
  <svg {...base(p)}>
    <circle cx="12" cy="12" r="9" />
    <path d="m8.5 12.5 2.5 2.5 4.5-5" />
  </svg>
);

export const IconChevronDown = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="m6 9.5 6 6 6-6" />
  </svg>
);

export const IconChevronRight = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="m9.5 6 6 6-6 6" />
  </svg>
);

export const IconChevronLeft = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="m14.5 6-6 6 6 6" />
  </svg>
);

export const IconClose = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M6 6l12 12M18 6 6 18" />
  </svg>
);

export const IconUpload = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M12 19V5" />
    <path d="m6 11 6-6 6 6" />
  </svg>
);

export const IconDownload = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M12 5v14" />
    <path d="m6 13 6 6 6-6" />
  </svg>
);

export const IconShield = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M12 3 5 6v5.5c0 4.5 3 8 7 9.5 4-1.5 7-5 7-9.5V6z" />
    <path d="m9.5 11.5 2 2 3.5-4" />
  </svg>
);

export const IconShuffle = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M3 7h4l10 10h4" />
    <path d="M3 17h4l3-3" />
    <path d="m14 10 3-3h4" />
    <path d="m18 4 3 3-3 3" />
    <path d="m18 14 3 3-3 3" />
  </svg>
);

export const IconRule = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M6 4v16" />
    <path d="M6 7h9M6 13h9" />
    <circle cx="18" cy="7" r="2.4" />
    <circle cx="18" cy="13" r="2.4" />
  </svg>
);

export const IconTarget = (p: IconProps) => (
  <svg {...base(p)}>
    <circle cx="12" cy="12" r="8.5" />
    <circle cx="12" cy="12" r="4.5" />
    <circle cx="12" cy="12" r="1" fill="currentColor" stroke="none" />
  </svg>
);

export const IconClock = (p: IconProps) => (
  <svg {...base(p)}>
    <circle cx="12" cy="12" r="8.5" />
    <path d="M12 7v5l3.2 2" />
  </svg>
);

export const IconSettings = (p: IconProps) => (
  <svg {...base(p)}>
    <circle cx="12" cy="12" r="3.2" />
    <path d="M19 12a7 7 0 0 0-.14-1.4l2.1-1.6-2-3.4-2.5 1a7 7 0 0 0-2.4-1.4L13.6 2h-3.2l-.5 3.2a7 7 0 0 0-2.4 1.4l-2.5-1-2 3.4 2.1 1.6A7 7 0 0 0 5 12c0 .5.05.9.14 1.4L3 15l2 3.4 2.5-1a7 7 0 0 0 2.4 1.4l.5 3.2h3.2l.5-3.2a7 7 0 0 0 2.4-1.4l2.5 1 2-3.4-2.1-1.6c.1-.5.1-.9.1-1.4z" />
  </svg>
);

export const IconFolder = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M3 7a2 2 0 0 1 2-2h4l2 2.5h8a2 2 0 0 1 2 2V17a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
  </svg>
);

export const IconCopy = (p: IconProps) => (
  <svg {...base(p)}>
    <rect x="9" y="9" width="12" height="12" rx="2" />
    <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" transform="translate(1 0)" />
  </svg>
);

export const IconLanguage = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M3 5h10" />
    <path d="M8 3v2c0 4-2.5 7-5 8.5" />
    <path d="M5 8.5C6.5 11.5 9 13.5 12 14.5" />
    <path d="m13 21 4-9 4 9" />
    <path d="M14.2 18h5.6" />
  </svg>
);

export const IconBrush = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="m15 5 4 4" />
    <path d="M17.5 2.5a2.1 2.1 0 0 1 3 3L8 18l-4 1 1-4z" />
  </svg>
);

export const IconCloud = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M17.5 19a4.5 4.5 0 0 0 .4-9A6 6 0 0 0 6.2 9.5 4.5 4.5 0 0 0 7 19z" />
    <path d="M12 12v5" />
    <path d="m9.8 14.8 2.2-2.3 2.2 2.3" />
  </svg>
);

export const IconKeyboard = (p: IconProps) => (
  <svg {...base(p)}>
    <rect x="3" y="6" width="18" height="12" rx="2" />
    <path d="M7 10h.01M11 10h.01M15 10h.01M7 14h10" />
  </svg>
);

export const IconWrench = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M14.7 6.3a4.5 4.5 0 0 0-6 6L3 18a2.1 2.1 0 0 0 3 3l5.7-5.7a4.5 4.5 0 0 0 6-6L14.5 12 12 9.5z" />
  </svg>
);

export const IconSort = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M4 7h12M4 12h9M4 17h6" />
    <path d="M17 13v6M17 19l2.5-2.5M17 19l-2.5-2.5" />
  </svg>
);

export const IconKebab = (p: IconProps) => (
  <svg {...base(p)}>
    <circle cx="12" cy="5.5" r="1.4" fill="currentColor" stroke="none" />
    <circle cx="12" cy="12" r="1.4" fill="currentColor" stroke="none" />
    <circle cx="12" cy="18.5" r="1.4" fill="currentColor" stroke="none" />
  </svg>
);

export const IconLocation = (p: IconProps) => (
  <svg {...base(p)}>
    <circle cx="12" cy="12" r="3" />
    <path d="M12 2v3M12 19v3M2 12h3M19 12h3" />
    <circle cx="12" cy="12" r="8.5" strokeDasharray="3 3" />
  </svg>
);

export const IconArrowSwap = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M8 3 4 7l4 4" />
    <path d="M4 7h13" />
    <path d="m16 13 4 4-4 4" />
    <path d="M20 17H7" />
  </svg>
);

export const IconMemory = (p: IconProps) => (
  <svg {...base(p)}>
    <rect x="5" y="8" width="14" height="10" rx="2" />
    <path d="M8 8V5M12 8V5M16 8V5" />
    <path d="M8 18v1.5M12 18v1.5M16 18v1.5" />
  </svg>
);

export const IconInfo = (p: IconProps) => (
  <svg {...base(p)}>
    <circle cx="12" cy="12" r="9" />
    <path d="M12 8h.01" />
    <path d="M12 11v5" />
  </svg>
);

export const IconMenu = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M4 7h16M4 12h16M4 17h16" />
  </svg>
);

export const IconPencil = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M21.17 6.81a1 1 0 0 0-3.98-3.99L3.84 16.17a2 2 0 0 0-.5.83l-1.32 4.35a.5.5 0 0 0 .62.62l4.35-1.32a2 2 0 0 0 .83-.5z" />
    <path d="m15 5 4 4" />
  </svg>
);

export const IconQrCode = (p: IconProps) => (
  <svg {...base(p)}>
    <rect x="3" y="3" width="5" height="5" rx="1" />
    <rect x="16" y="3" width="5" height="5" rx="1" />
    <rect x="3" y="16" width="5" height="5" rx="1" />
    <path d="M21 16h-3a2 2 0 0 0-2 2v3" />
    <path d="M21 21h.01" />
    <path d="M12 7v3a2 2 0 0 1-2 2H7" />
    <path d="M3 12h.01" />
    <path d="M12 3h.01" />
    <path d="M12 16v.01" />
    <path d="M16 12h1" />
    <path d="M21 12h.01" />
    <path d="M12 21v-1" />
  </svg>
);

export const IconImport = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M3 6h13M3 12h13M3 18h7" />
    <path d="M18 14v6M15 17h6" />
  </svg>
);

export const IconBot = (p: IconProps) => (
  <svg {...base(p)}>
    <rect x="4" y="8" width="16" height="12" rx="3.5" />
    <path d="M12 8V4.5" />
    <circle cx="12" cy="3.6" r="1" />
    <path d="M9 13.5v1.5M15 13.5v1.5" />
    <path d="M2 13v2M22 13v2" />
  </svg>
);

export const IconKey = (p: IconProps) => (
  <svg {...base(p)}>
    <circle cx="7.5" cy="15.5" r="4.5" />
    <path d="m10.7 12.3 9.3-9.3" />
    <path d="m16 7 3 3M14 9l2 2" />
  </svg>
);

export const IconTerminal = (p: IconProps) => (
  <svg {...base(p)}>
    <rect x="3" y="4" width="18" height="16" rx="2.5" />
    <path d="m7 9 3 3-3 3M12.5 15H17" />
  </svg>
);

export const IconEye = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12z" />
    <circle cx="12" cy="12" r="2.8" />
  </svg>
);

export const IconEyeOff = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M10.6 5.6A9.6 9.6 0 0 1 12 5.5C18 5.5 21.5 12 21.5 12a16 16 0 0 1-2.7 3.5M6.6 6.6C3.9 8.3 2.5 12 2.5 12S6 18.5 12 18.5a9 9 0 0 0 4.4-1.1" />
    <path d="M9.9 9.9a2.8 2.8 0 0 0 4.2 4.2" />
    <path d="m3 3 18 18" />
  </svg>
);

export const IconPlug = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M9 3v5M15 3v5" />
    <path d="M6 8h12v3a6 6 0 0 1-12 0z" />
    <path d="M12 17v4" />
  </svg>
);

export const IconBook = (p: IconProps) => (
  <svg {...base(p)}>
    <path d="M4 19.5V5a2 2 0 0 1 2-2h13v15H6a2 2 0 0 0-2 2z" />
    <path d="M4 19.5A1.5 1.5 0 0 0 5.5 21H19" />
    <path d="M8.5 7.5h7M8.5 11h5" />
  </svg>
);
