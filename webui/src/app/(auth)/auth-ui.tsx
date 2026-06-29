"use client";

import Image from "next/image";
import * as React from "react";
import { Eye, EyeOff } from "lucide-react";
import { Button, type ButtonProps } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { useSystemStore } from "@/store/system";
import { SITE_NAME } from "@/lib/site-config";
import { sanitizeImageUrl } from "@/lib/safe-url";
import { API_BASE } from "@/lib/api-request";

export const AUTH_PRIMARY_BTN =
  "auth-ios-glass auth-ios-button h-[52px] w-full px-4 disabled:opacity-50";

export const AUTH_SECONDARY_BTN =
  "auth-ios-glass auth-ios-link-button h-11 px-4 text-sm disabled:opacity-50";

export const AUTH_ICON_BTN =
  "auth-ios-glass auth-ios-link-button h-11 w-11 p-0 text-sm disabled:opacity-50";

export const AUTH_TAB_LIST =
  "auth-ios-glass flex w-full rounded-[999px] p-1";

export const AUTH_TAB_TRIGGER =
  "auth-ios-tab-trigger rounded-[999px] px-4 py-2 text-sm transition-all duration-300";

export const AUTH_GHOST_LINK =
  "font-medium text-[#4b4a72]/85 underline underline-offset-4 decoration-white/45 transition-colors hover:text-[#7c3aed]";

export const AUTH_CHIP_LINK =
  "auth-ios-glass inline-flex items-center rounded-[999px] px-3 py-1.5 text-xs font-semibold text-[#2f2a56] transition-all duration-300 hover:text-[#241f4f]";

export const AUTH_LABEL =
  "auth-ios-label text-sm";

export const AUTH_INPUT =
  "auth-ios-input";

export const AUTH_FIELD =
  "auth-ios-glass auth-ios-input-wrap w-full transition-all duration-300 focus-within:border-white/50 focus-within:shadow-[0_14px_40px_rgba(50,45,140,0.22)]";

export const AUTH_PASSWORD_TOGGLE =
  "absolute right-2 top-1/2 z-10 flex h-9 w-9 -translate-y-1/2 items-center justify-center rounded-full text-[#2f2a56]/78 transition-all duration-300 hover:text-[#241f4f]";

export const AUTH_SUPPORT_TEXT = "auth-ios-text text-sm";

export const AUTH_HINT_TEXT = "auth-ios-text text-xs";

export const AUTH_NOTICE_CARD =
  "auth-ios-glass rounded-[28px] px-4 py-3 text-sm text-[#2f2a56]";

export const AUTH_STATUS_CARD =
  "auth-ios-glass rounded-[28px] px-4 py-3 text-sm text-[#2f2a56]";

export function AuthLiquidDefs() {
  return (
    <svg className="pointer-events-none absolute h-0 w-0" aria-hidden="true" focusable="false">
      <defs>
        <filter id="auth-liquid-filter" x="-20%" y="-20%" width="140%" height="140%">
          <feTurbulence
            type="fractalNoise"
            baseFrequency="0.015 0.02"
            numOctaves="2"
            seed="9"
            result="noise"
          />
          <feGaussianBlur in="noise" stdDeviation="1.2" result="blurredNoise" />
          <feDisplacementMap
            in="SourceGraphic"
            in2="blurredNoise"
            scale="3"
            xChannelSelector="R"
            yChannelSelector="G"
          />
        </filter>
      </defs>
    </svg>
  );
}

export function AuthFieldShell({ className, children }: { className?: string; children: React.ReactNode }) {
  return <div className={cn(AUTH_FIELD, className)}>{children}</div>;
}

type AuthTextInputProps = Omit<React.InputHTMLAttributes<HTMLInputElement>, "size"> & {
  shellClassName?: string;
};

export const AuthTextInput = React.forwardRef<HTMLInputElement, AuthTextInputProps>(
  ({ className, shellClassName, ...props }, ref) => (
    <AuthFieldShell className={shellClassName}>
      <input ref={ref} className={cn(AUTH_INPUT, className)} {...props} />
    </AuthFieldShell>
  ),
);
AuthTextInput.displayName = "AuthTextInput";

type AuthPasswordFieldProps = Omit<AuthTextInputProps, "type"> & {
  visible?: boolean;
  onToggleVisible?: () => void;
  toggleLabel?: string;
};

export const AuthPasswordField = React.forwardRef<HTMLInputElement, AuthPasswordFieldProps>(
  ({ className, visible, onToggleVisible, toggleLabel, ...props }, ref) => {
    const [internalVisible, setInternalVisible] = React.useState(false);
    const resolvedVisible = visible ?? internalVisible;
    const handleToggle = () => {
      if (onToggleVisible) {
        onToggleVisible();
        return;
      }
      setInternalVisible((value) => !value);
    };

    return (
      <div className="relative">
        <AuthTextInput
          ref={ref}
          type={resolvedVisible ? "text" : "password"}
          className={cn("pr-12", className)}
          {...props}
        />
        <button
          type="button"
          onClick={handleToggle}
          className={AUTH_PASSWORD_TOGGLE}
          aria-label={toggleLabel || "切换密码可见性"}
        >
          {resolvedVisible ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
        </button>
      </div>
    );
  },
);
AuthPasswordField.displayName = "AuthPasswordField";

export function AuthPrimaryButton({ className, ...props }: ButtonProps) {
  return <Button className={cn(AUTH_PRIMARY_BTN, className)} {...props} />;
}

export function AuthSecondaryButton({ className, ...props }: ButtonProps) {
  return <Button className={cn(AUTH_SECONDARY_BTN, className)} {...props} />;
}

export function AuthIconButton({ className, ...props }: ButtonProps) {
  return <Button className={cn(AUTH_ICON_BTN, className)} {...props} />;
}

// 通过 NEXT_PUBLIC_AUTH_ICON_URL 可覆盖默认的站点图标（优先于后端返回的 server_icon）。
const envIcon = process.env.NEXT_PUBLIC_AUTH_ICON_URL;
const defaultAuthIcon = "/brand/logo.png";

function serverIconUrl(icon?: string | null): string | undefined {
  if (!icon) return undefined;
  if (icon.startsWith("http")) return sanitizeImageUrl(icon);
  if (icon.startsWith("/api/v1/")) return sanitizeImageUrl(`${API_BASE}${icon}`);
  if (icon.startsWith("/system/")) return sanitizeImageUrl(`${API_BASE}/api/v1${icon}`);
  if (icon.startsWith("/")) return sanitizeImageUrl(icon);
  return sanitizeImageUrl(icon);
}

// AuthBrand 渲染站点图标 + 名称，作为认证表单顶部的统一品牌头。
export function AuthBrand({ subtitle }: { subtitle?: string }) {
  const { info } = useSystemStore();
  const name = info?.name || SITE_NAME;
  // envIcon 优先级高于后端 server_icon
  const icon =
    serverIconUrl(envIcon || defaultAuthIcon) ||
    serverIconUrl(info?.icon || info?.server_icon);

  return (
    <div className="flex flex-col items-center gap-3 text-center">
      {icon ? (
        <div className="auth-ios-glass relative h-16 w-16 overflow-hidden rounded-[24px] shadow-[0_12px_36px_rgba(30,40,120,0.20)]">
          <Image
            src={icon}
            alt={name}
            fill
            className="rounded-2xl object-cover"
            onError={(e) => {
              e.currentTarget.style.display = "none";
            }}
          />
        </div>
      ) : (
        <div className="auth-ios-glass flex h-16 w-16 items-center justify-center rounded-[24px] text-lg font-bold text-[#241f4f] shadow-[0_12px_36px_rgba(30,40,120,0.20)]">
          {name.slice(0, 2).toUpperCase()}
        </div>
      )}
      <div className="space-y-1">
        <h1 className="text-2xl font-bold tracking-tight text-[#2d2758] drop-shadow-[0_2px_10px_rgba(255,255,255,0.72)]">
          {name}
        </h1>
        {subtitle ? (
          <p className="text-sm text-[#4e4878]/85 drop-shadow-[0_1px_6px_rgba(255,255,255,0.58)]">
            {subtitle}
          </p>
        ) : null}
      </div>
    </div>
  );
}

// AuthPanel 是认证页浮层外壳：桌面端偏右，移动端居中。
// children 由各页面提供具体表单内容。
export function AuthPanel({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <main className="relative flex min-h-dvh w-full">
      <section className={cn("auth-panel animate-auth-enter", className)}>
        <div className="auth-panel-inner space-y-7">{children}</div>
      </section>
    </main>
  );
}

// AuthStepDots 渲染注册向导的步骤进度点。
export function AuthStepDots({ total, current }: { total: number; current: number }) {
  return (
    <div className="flex items-center justify-center gap-1.5" aria-hidden="true">
      {Array.from({ length: total }).map((_, i) => (
        <span
          key={i}
          className={cn(
            "auth-step-dot",
            i === current && "auth-step-dot-active",
            i < current && "auth-step-dot-done",
          )}
        />
      ))}
    </div>
  );
}
