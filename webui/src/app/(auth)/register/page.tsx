"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  Loader2,
  ShieldPlus,
  UserPlus,
  Bot,
  Send,
  ArrowLeft,
  ArrowRight,
} from "lucide-react";
import { Label } from "@/components/ui/label";
import { useToast } from "@/hooks/use-toast";
import { api, type RegisterAvailability, type RegisterData } from "@/lib/api";
import { ApiError } from "@/lib/api-request";
import { ErrCodes } from "@/lib/errcode";
import { useSystemStore } from "@/store/system";
import { passwordStrengthLabel, validatePasswordStrength } from "@/lib/password";
import { friendlyError, validateUsername } from "@/lib/validators";
import { sanitizeExternalUrl, telegramBotUrl } from "@/lib/safe-url";
import { useI18n } from "@/lib/i18n";
import {
  AuthBrand,
  AUTH_CHIP_LINK,
  AuthPasswordField,
  AuthPrimaryButton,
  AuthSecondaryButton,
  AuthStepDots,
  AuthTextInput,
  AUTH_GHOST_LINK,
  AUTH_HINT_TEXT,
  AUTH_LABEL,
  AUTH_NOTICE_CARD,
  AUTH_STATUS_CARD,
  AUTH_SUPPORT_TEXT,
} from "../auth-ui";

type RegisterBindCodeStatusMessage = {
  type?: string;
  code?: string;
  status?: string;
  error_code?: string;
  message?: string;
  confirmed?: boolean;
  expires_in?: number;
  invalid?: boolean;
  terminal?: boolean;
  telegram_bound?: boolean;
  telegram_id?: number;
  telegram_username?: string;
};

export default function RegisterPage() {
  const router = useRouter();
  const { toast } = useToast();
  const { t } = useI18n();
  const { info: systemInfo } = useSystemStore();

  // --- Account form state ---
  const [formData, setFormData] = useState({
    username: "",
    password: "",
    confirmPassword: "",
    email: "",
    regCode: "",
  });
  const [showPassword, setShowPassword] = useState(false);
  const [registerAvailability, setRegisterAvailability] =
    useState<RegisterAvailability | null>(null);

  // --- Telegram binding state ---
  const [bindCode, setBindCode] = useState("");
  const [bindCodeExpiry, setBindCodeExpiry] = useState(0);
  const [bindConfirmed, setBindConfirmed] = useState(false);
  const [isBindCodeLoading, setIsBindCodeLoading] = useState(false);

  // --- Submission ---
  const [isRegisterLoading, setIsRegisterLoading] = useState(false);

  // --- Wizard ---
  const hasTelegramStep = Boolean(
    systemInfo?.features?.force_bind_telegram || systemInfo?.features?.telegram,
  );
  const forceBindTelegram = Boolean(systemInfo?.features?.force_bind_telegram);
  const emailEnabled = Boolean(systemInfo?.features?.email_enabled);
  const TOTAL_STEPS = hasTelegramStep ? 2 : 1;
  const [step, setStep] = useState(0);

  // Derived
  const registerRequiresCode = Boolean(
    registerAvailability?.requires_reg_code && (registerAvailability?.current_users ?? 0) > 0,
  );
  const canRegister =
    registerAvailability?.can_register ?? registerAvailability?.available ?? true;

  // Telegram links
  const requiredTelegramLinks = [
    ...(systemInfo?.required_telegram_links?.groups || []),
    ...(systemInfo?.required_telegram_links?.channels || []),
  ];
  const telegramLinks = [
    ...(requiredTelegramLinks.length > 0
      ? requiredTelegramLinks
      : [
          ...(systemInfo?.telegram_links?.groups || []),
          ...(systemInfo?.telegram_links?.channels || []),
        ]),
  ]
    .map((item) => ({ ...item, url: sanitizeExternalUrl(item.url) }))
    .filter((item): item is { label: string; url: string } => Boolean(item.url));
  const botUsername = systemInfo?.telegram_bot?.username;
  const botUrl = telegramBotUrl(systemInfo?.telegram_bot?.username, systemInfo?.telegram_bot?.url);

  // Init
  useEffect(() => {
    void refreshRegisterAvailability();
  }, []);

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setFormData((p) => ({ ...p, [e.target.name]: e.target.value }));
  };

  const refreshRegisterAvailability = async () => {
    try {
      const res = await api.getRegisterAvailability();
      if (res.success && res.data) setRegisterAvailability(res.data);
    } catch {
      // ignore
    }
  };

  // ---- Telegram binding (unchanged logic from original) ----

  const handleGetTelegramBindCode = async () => {
    setIsBindCodeLoading(true);
    try {
      const res = await api.getRegisterBindCode();
      setBindCode(res.data?.bind_code || "");
      setBindCodeExpiry(res.data?.expires_in ?? 0);
      setBindConfirmed(false);
      toast({
        title: t("auth.register.bindCodeGenerated"),
        description: t("auth.register.bindCodeGeneratedDescription"),
        variant: "success",
      });
    } catch (error: any) {
      toast({
        title: t("auth.register.bindCodeFailed"),
        description: error.message || t("auth.register.bindCodeFailedDescription"),
        variant: "destructive",
      });
    } finally {
      setIsBindCodeLoading(false);
    }
  };

  // WebSocket waits for Bot confirmation
  useEffect(() => {
    if (!bindCode || bindConfirmed) return;

    let cancelled = false;
    let toastedConfirmed = false;
    let retryTimer: number | null = null;
    let socket: WebSocket | null = null;
    let terminal = false;

    const stopWithToast = (title: string, description: string) => {
      terminal = true;
      setBindCode("");
      setBindCodeExpiry(0);
      setBindConfirmed(false);
      toast({ title, description, variant: "destructive" });
    };

    const markConfirmed = () => {
      terminal = true;
      if (!toastedConfirmed) {
        toastedConfirmed = true;
        setBindConfirmed(true);
        toast({
          title: t("auth.register.telegramBound"),
          description: t("auth.register.telegramBoundDescription"),
          variant: "success",
        });
      }
    };

    const handleStatus = (data: RegisterBindCodeStatusMessage) => {
      if (typeof data.expires_in === "number") setBindCodeExpiry(data.expires_in);
      if (!data.terminal) return;
      if (data.status === "confirmed" || (data.confirmed && !data.invalid)) {
        markConfirmed();
        return;
      }
      const description =
        friendlyError(data.error_code, data.message) ||
        data.message ||
        t("auth.register.retryBindCode");
      stopWithToast(t("auth.register.telegramIncomplete"), description);
    };

    const connect = () => {
      if (cancelled || terminal) return;
      try {
        socket = new WebSocket(api.getRegisterBindCodeStatusWebSocketUrl(bindCode));
      } catch (error) {
        stopWithToast(
          t("auth.register.bindStatusFailed"),
          error instanceof Error ? error.message : t("auth.register.websocketFailed"),
        );
        return;
      }
      socket.onmessage = (event) => {
        if (cancelled) return;
        try {
          handleStatus(JSON.parse(String(event.data)) as RegisterBindCodeStatusMessage);
        } catch {
          // ignore unrecognised frames
        }
      };
      socket.onerror = () => socket?.close();
      socket.onclose = () => {
        if (cancelled || terminal || bindConfirmed) return;
        retryTimer = window.setTimeout(connect, 2000);
      };
    };

    connect();

    return () => {
      cancelled = true;
      if (retryTimer !== null) window.clearTimeout(retryTimer);
      socket?.close();
    };
  }, [bindCode, bindConfirmed, t, toast]);

  const refreshBindConfirmedBeforeSubmit = async (): Promise<boolean> => {
    if (!bindCode) return false;
    try {
      const res = await api.getRegisterBindCodeStatus(bindCode);
      if (res.data?.status === "confirmed" || (res.data?.confirmed && !res.data.invalid)) {
        setBindConfirmed(true);
        return true;
      }
      if (res.data?.terminal && res.data.invalid) {
        const description =
          friendlyError(res.data.error_code, res.data.message) ||
          res.data.message ||
          t("auth.register.retryGetBindCode");
        setBindCode("");
        setBindCodeExpiry(0);
        toast({ title: t("auth.register.telegramIncomplete"), description, variant: "destructive" });
      }
    } catch {
      // network blip → keep current state
    }
    return false;
  };

  // ---- Validation ----

  const validateAccountStep = (): boolean => {
    const uc = validateUsername(formData.username);
    if (!uc.ok) {
      toast({ title: t("auth.register.invalidUsername"), description: uc.message, variant: "destructive" });
      return false;
    }
    if (registerAvailability && (!canRegister || !registerAvailability.available)) {
      toast({
        title: t("auth.register.unavailable"),
        description: registerAvailability.message,
        variant: "destructive",
      });
      return false;
    }
    if (registerRequiresCode && !formData.regCode.trim()) {
      toast({
        title: t("auth.register.regCodeRequired"),
        description: t("auth.register.regCodeRequiredDescription"),
        variant: "destructive",
      });
      return false;
    }
    if (!formData.password) {
      toast({ title: t("auth.register.passwordRequired"), variant: "destructive" });
      return false;
    }
    if (formData.password !== formData.confirmPassword) {
      toast({
        title: t("auth.register.passwordMismatch"),
        description: t("auth.register.passwordMismatchDescription"),
        variant: "destructive",
      });
      return false;
    }
    const strength = validatePasswordStrength(formData.password, t("common.password"));
    if (!strength.ok) {
      toast({ title: t("auth.register.passwordWeak"), description: strength.message, variant: "destructive" });
      return false;
    }
    return true;
  };

  // ---- Navigation ----

  const goNext = () => {
    if (!validateAccountStep()) return;
    // If only 1 step or no Telegram step → submit directly
    if (TOTAL_STEPS === 1) {
      void doSubmit();
      return;
    }
    setStep(1);
  };

  const goBack = () => setStep(0);

  const skipTelegramAndSubmit = () => {
    void doSubmit();
  };

  const handleFinalSubmit = async () => {
    if (bindCode && !bindConfirmed) {
      const confirmed = await refreshBindConfirmedBeforeSubmit();
      if (!confirmed) {
        toast({
          title: t("auth.register.telegramCompleteBeforeSubmit"),
          description: t("auth.register.sendBindCommand", { code: bindCode }),
          variant: "destructive",
        });
        return;
      }
    }
    void doSubmit();
  };

  const doSubmit = async () => {
    setIsRegisterLoading(true);
    try {
      const payload: RegisterData = {
        username: formData.username.trim(),
        email: formData.email || undefined,
        telegram_bind_code: bindCode || undefined,
        password: formData.password,
        reg_code: registerRequiresCode ? formData.regCode.trim() : undefined,
      };
      const res = await api.register(payload);
      if (!res.success) {
        toast({
          title: t("auth.register.failed"),
          description:
            res.error_code === ErrCodes.UsernameTaken
              ? t("auth.register.usernameTaken")
              : res.message,
          variant: "destructive",
        });
        return;
      }
      toast({
        title: t("auth.register.success"),
        description: t("auth.register.successDescription"),
        variant: "success",
      });
      router.push("/login");
    } catch (error: any) {
      const message =
        error instanceof ApiError && error.errorCode === ErrCodes.UsernameTaken
          ? t("auth.register.usernameTaken")
          : error.message || t("common.checkNetwork");
      toast({ title: t("auth.register.failed"), description: message, variant: "destructive" });
    } finally {
      setIsRegisterLoading(false);
      void refreshRegisterAvailability();
    }
  };

  // ---- Step 0: Account form ----

  const step0 = (
    <>
      <div className={`grid grid-cols-1 gap-4 ${emailEnabled ? "sm:grid-cols-2" : ""}`}>
        <div className="space-y-2">
          <Label htmlFor="username" className={AUTH_LABEL}>
            {t("auth.register.requiredUsername")}
          </Label>
          <AuthTextInput
            id="username"
            name="username"
            placeholder="Username"
            value={formData.username}
            onChange={handleChange}
            autoComplete="username"
          />
        </div>
        {emailEnabled && (
          <div className="space-y-2">
            <Label htmlFor="email" className={AUTH_LABEL}>
              {t("common.email")}
            </Label>
            <AuthTextInput
              id="email"
              name="email"
              type="email"
              placeholder="Email (Optional)"
              value={formData.email}
              onChange={handleChange}
              autoComplete="email"
            />
          </div>
        )}
      </div>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <Label htmlFor="password" className={AUTH_LABEL}>
            {t("auth.register.passwordLabel")}
          </Label>
          <AuthPasswordField
            id="password"
            name="password"
            visible={showPassword}
            onToggleVisible={() => setShowPassword(!showPassword)}
            toggleLabel={t("common.showPassword")}
            placeholder={t("auth.register.passwordPlaceholder")}
            value={formData.password}
            onChange={handleChange}
            autoComplete="new-password"
          />
          {formData.password &&
            (() => {
              const s = validatePasswordStrength(formData.password, t("common.password"));
              return (
                <p
                  className={`text-xs ${
                    s.ok ? passwordStrengthLabel(s.score).className : "text-[#c0466f]"
                  }`}
                >
                  {s.ok
                    ? t("auth.register.passwordStrength", {
                        label: passwordStrengthLabel(s.score).label,
                      })
                    : s.message}
                </p>
              );
            })()}
        </div>
        <div className="space-y-2">
          <Label htmlFor="confirmPassword" className={AUTH_LABEL}>
            {t("auth.register.confirmPassword")}
          </Label>
          <AuthPasswordField
            id="confirmPassword"
            name="confirmPassword"
            placeholder="Confirm Password"
            value={formData.confirmPassword}
            onChange={handleChange}
            autoComplete="new-password"
          />
        </div>
      </div>

      {registerRequiresCode && (
        <div className="space-y-2">
          <Label htmlFor="regCode" className={AUTH_LABEL}>
            {t("auth.register.regCodeLabel")}
          </Label>
          <AuthTextInput
            id="regCode"
            name="regCode"
            placeholder={t("auth.register.regCodePlaceholder")}
            value={formData.regCode}
            onChange={handleChange}
            className="font-mono"
          />
          <p className={AUTH_HINT_TEXT}>
            {t("auth.register.regCodeConsumptionHint")}
          </p>
        </div>
      )}

      {registerAvailability ? (
        <p className={`text-center ${AUTH_HINT_TEXT}`}>
          {registerAvailability.max_users <= 0
            ? t("auth.register.quotaUnlimited", {
                current: registerAvailability.current_users,
              })
            : t("auth.register.quota", {
                current: registerAvailability.current_users,
                max: registerAvailability.max_users,
              })}
        </p>
      ) : null}

      <AuthPrimaryButton
        type="button"
        onClick={goNext}
        disabled={
          Boolean(registerAvailability && (!canRegister || !registerAvailability.available))
        }
      >
        {TOTAL_STEPS > 1 ? (
          <>
            {t("auth.register.stepNext")}
            <ArrowRight className="ml-2 h-5 w-5" />
          </>
        ) : (
          <>
            <UserPlus className="mr-2 h-5 w-5" />
            {t("auth.register.submit")}
          </>
        )}
      </AuthPrimaryButton>
    </>
  );

  // ---- Step 1: Telegram binding ----

  const step1 = (
    <>
      <div className="space-y-2">
        <Label className={AUTH_LABEL}>
          {t("auth.register.telegramBinding", {
            suffix: forceBindTelegram ? " *" : t("common.optional"),
          })}
        </Label>
        <div className={AUTH_NOTICE_CARD}>
          <p className="font-medium">{t("auth.register.openBotChat")}</p>
          <p className="mt-1 leading-relaxed">{t("auth.register.bindInstructions")}</p>
          {botUsername ? (
            <p className={`mt-2 inline-flex items-center gap-1.5 ${AUTH_HINT_TEXT}`}>
              <Bot className="h-3.5 w-3.5" />
              <span>{t("auth.register.siteBot")}</span>
              <a
                href={botUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="font-medium text-[#6a54ff] underline-offset-2 hover:underline"
              >
                @{botUsername}
              </a>
            </p>
          ) : (
            <p className={`mt-2 ${AUTH_HINT_TEXT}`}>
              {t("auth.register.botNotConfigured")}
            </p>
          )}
        </div>
      </div>

      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:flex-wrap">
        <AuthPrimaryButton
          type="button"
          onClick={handleGetTelegramBindCode}
          disabled={isBindCodeLoading}
        >
          {isBindCodeLoading ? (
            <Loader2 className="mr-2 h-4 w-4 animate-spin" />
          ) : (
            <ShieldPlus className="mr-2 h-4 w-4" />
          )}
          {t("auth.register.getBindCode")}
        </AuthPrimaryButton>
        {botUrl ? (
          <AuthSecondaryButton asChild type="button">
            <a href={botUrl} target="_blank" rel="noopener noreferrer">
              <Bot className="mr-2 h-4 w-4" />
              {t("auth.register.openBot", { username: botUsername })}
            </a>
          </AuthSecondaryButton>
        ) : null}
      </div>

      {bindCode && !bindConfirmed ? (
        <div className={`${AUTH_STATUS_CARD} space-y-2`}>
          <p>{t("auth.register.sendCommandBelow")}</p>
          <div className="flex flex-wrap items-center gap-2">
            <code className="rounded-2xl border border-white/30 bg-white/14 px-3 py-1.5 font-mono text-base text-[#2d2758] shadow-[0_8px_22px_rgba(120,130,255,0.12)] select-all break-all max-w-full">
              /bind {bindCode}
            </code>
            <AuthSecondaryButton
              type="button"
              size="sm"
              onClick={() => {
                navigator.clipboard.writeText(`/bind ${bindCode}`).then(
                  () => toast({ title: t("common.copiedToClipboard"), variant: "success" }),
                  () => toast({ title: t("common.copyFailed"), variant: "destructive" }),
                );
              }}
            >
              {t("auth.register.copyCommand")}
            </AuthSecondaryButton>
            {botUrl ? (
              <AuthSecondaryButton asChild type="button" size="sm">
                <a href={botUrl} target="_blank" rel="noopener noreferrer">
                  <Bot className="mr-2 h-4 w-4" />
                  {t("auth.register.openBot", { username: botUsername })}
                </a>
              </AuthSecondaryButton>
            ) : null}
          </div>
          <p className={`flex items-center gap-1 ${AUTH_HINT_TEXT}`}>
            <Loader2 className="h-3 w-3 animate-spin" />
            {t("auth.register.waitingVerification", {
              minutes: Math.max(0, Math.floor(bindCodeExpiry / 60)),
            })}
          </p>
        </div>
      ) : null}

      {bindCode && bindConfirmed ? (
        <div className={AUTH_STATUS_CARD}>
          <p className="font-semibold text-[#3e9f76]">
            {t("auth.register.telegramBound")}
          </p>
          <p className="text-xs text-[#3e9f76]/85">
            {t("auth.register.telegramBoundDescription")}
          </p>
        </div>
      ) : null}

      {/* Final action row */}
      <div className="flex items-center gap-3 pt-2">
        <AuthSecondaryButton
          type="button"
          size="sm"
          onClick={goBack}
          className="shrink-0"
        >
          <ArrowLeft className="mr-1.5 h-4 w-4" />
          {t("auth.register.stepBack")}
        </AuthSecondaryButton>
        {!forceBindTelegram && (
          <AuthSecondaryButton
            type="button"
            size="sm"
            className="shrink-0"
            onClick={skipTelegramAndSubmit}
            disabled={isRegisterLoading}
          >
            {t("auth.register.stepSkip")}
          </AuthSecondaryButton>
        )}
        <AuthPrimaryButton
          type="button"
          onClick={handleFinalSubmit}
          disabled={
            isRegisterLoading ||
            (forceBindTelegram && !bindConfirmed) ||
            Boolean(registerAvailability && (!canRegister || !registerAvailability.available))
          }
        >
          {isRegisterLoading ? (
            <Loader2 className="mr-2 h-5 w-5 animate-spin" />
          ) : (
            <UserPlus className="mr-2 h-5 w-5" />
          )}
          {t("auth.register.submit")}
        </AuthPrimaryButton>
      </div>
    </>
  );

  // ---- Render ----

  return (
    <>
      {TOTAL_STEPS > 1 && <AuthStepDots total={TOTAL_STEPS} current={step} />}
      <AuthBrand
        subtitle={registerRequiresCode ? t("auth.register.introWithCode") : undefined}
      />

      {telegramLinks.length > 0 && step === 0 && (
        <div className={AUTH_NOTICE_CARD}>
          <div className="mb-2 flex items-center gap-2 font-semibold text-[#2d2758]">
            <Send className="h-4 w-4 text-[#6f6a93]" />
            {t("auth.register.telegramCommunity")}
          </div>
          <div className="flex flex-wrap gap-2">
            {telegramLinks.map((item) => (
              <a
                key={item.url}
                href={item.url}
                target="_blank"
                rel="noopener noreferrer"
                className={AUTH_CHIP_LINK}
              >
                {item.label}
              </a>
            ))}
          </div>
        </div>
      )}

      <div className="space-y-4">{step === 0 ? step0 : step1}</div>

      <div className="pt-1 text-center">
        <Link href="/login" className={`${AUTH_GHOST_LINK} ${AUTH_SUPPORT_TEXT}`}>
          {t("auth.register.backToLogin")}
        </Link>
      </div>
    </>
  );
}
