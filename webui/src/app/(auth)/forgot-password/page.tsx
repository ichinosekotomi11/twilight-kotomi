"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { Copy, Loader2, Mail } from "lucide-react";
import { Label } from "@/components/ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useToast } from "@/hooks/use-toast";
import { api } from "@/lib/api";
import {
  validateEmbyUsername,
  validateEmailOptional,
  friendlyError,
  isThrottleErrorCode,
  throttleCooldownSeconds,
} from "@/lib/validators";
import { validatePasswordStrength } from "@/lib/password";
import { useI18n } from "@/lib/i18n";
import { useSystemStore } from "@/store/system";
import {
  AuthBrand,
  AuthIconButton,
  AuthPasswordField,
  AuthPrimaryButton,
  AuthSecondaryButton,
  AuthTextInput,
  AUTH_GHOST_LINK,
  AUTH_HINT_TEXT,
  AUTH_LABEL,
  AUTH_NOTICE_CARD,
  AUTH_STATUS_CARD,
  AUTH_SUPPORT_TEXT,
  AUTH_TAB_LIST,
  AUTH_TAB_TRIGGER,
} from "../auth-ui";

export default function ForgotPasswordPage() {
  const { toast } = useToast();
  const { t } = useI18n();
  const { info: systemInfo } = useSystemStore();
  const features = systemInfo?.features;
  const forgotPasswordEnabled = Boolean(features?.forgot_password_enabled);
  const embyAvailable = Boolean(features?.forgot_password_emby_enabled);
  const emailAvailable =
    Boolean(features?.email_enabled) && Boolean(features?.forgot_password_email_enabled);

  // Emby
  const [embyUsername, setEmbyUsername] = useState("");
  const [embyPassword, setEmbyPassword] = useState("");
  const [embyLoading, setEmbyLoading] = useState(false);
  const [embyResult, setEmbyResult] = useState<{
    username: string;
    new_password: string;
  } | null>(null);

  // Email
  const [emailStage, setEmailStage] = useState<"request" | "reset">("request");
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [emailLoading, setEmailLoading] = useState(false);
  const [cooldown, setCooldown] = useState(0);
  const [emailDone, setEmailDone] = useState(false);

  useEffect(() => {
    if (cooldown <= 0) return;
    const timer = setInterval(() => setCooldown((s) => (s > 0 ? s - 1 : 0)), 1000);
    return () => clearInterval(timer);
  }, [cooldown]);

  // ---- Emby flow ----
  const submitEmby = async (e: React.FormEvent) => {
    e.preventDefault();
    const ch = validateEmbyUsername(embyUsername);
    if (!ch.ok) {
      toast({ title: ch.message, variant: "destructive" });
      return;
    }
    if (!embyPassword) {
      toast({ title: t("auth.forgotPassword.embyPasswordRequired"), variant: "destructive" });
      return;
    }
    setEmbyLoading(true);
    setEmbyResult(null);
    try {
      const res = await api.forgotPasswordByEmby({
        emby_username: embyUsername.trim(),
        emby_password: embyPassword,
      });
      if (res.success && res.data) {
        setEmbyResult(res.data);
        setEmbyPassword("");
        toast({
          title: t("auth.forgotPassword.resetSuccess"),
          description: t("auth.forgotPassword.oneTimePassword"),
          variant: "success",
        });
      } else {
        toast({
          title: t("auth.forgotPassword.failed"),
          description: friendlyError(res.error_code, res.message),
          variant: "destructive",
        });
      }
    } catch (error: any) {
      toast({
        title: t("auth.forgotPassword.failed"),
        description: error?.message || t("common.networkError"),
        variant: "destructive",
      });
    } finally {
      setEmbyLoading(false);
    }
  };

  // ---- Email flow ----
  const requestEmailCode = async (e?: React.FormEvent) => {
    e?.preventDefault();
    const ch = validateEmailOptional(email);
    if (!email || !ch.ok) {
      toast({ title: ch.message || t("email.enterEmailFirst"), variant: "destructive" });
      return;
    }
    setEmailLoading(true);
    try {
      const res = await api.requestPasswordResetEmail(email.trim());
      if (res.success) {
        setEmailStage("reset");
        setCooldown(res.data?.resend_after || 60);
        toast({ title: t("email.forgot.requestSent"), variant: "success" });
      } else if (isThrottleErrorCode(res.error_code)) {
        setCooldown((c) => (c > 0 ? c : throttleCooldownSeconds(res.error_code)));
        toast({ title: friendlyError(res.error_code, res.message), variant: "destructive" });
      } else {
        toast({ title: friendlyError(res.error_code, res.message), variant: "destructive" });
      }
    } catch (error: any) {
      toast({ title: error?.message || t("common.networkError"), variant: "destructive" });
    } finally {
      setEmailLoading(false);
    }
  };

  const submitEmailReset = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!code.trim()) {
      toast({ title: t("email.enterCodeFirst"), variant: "destructive" });
      return;
    }
    const strength = validatePasswordStrength(newPassword, t("common.password"));
    if (!strength.ok) {
      toast({ title: strength.message, variant: "destructive" });
      return;
    }
    setEmailLoading(true);
    try {
      const res = await api.resetPasswordByEmail({
        email: email.trim(),
        code: code.trim(),
        new_password: newPassword,
      });
      if (res.success) {
        setEmailDone(true);
        toast({ title: t("email.forgot.resetSuccess"), variant: "success" });
      } else {
        toast({ title: friendlyError(res.error_code, res.message), variant: "destructive" });
      }
    } catch (error: any) {
      toast({ title: error?.message || t("common.networkError"), variant: "destructive" });
    } finally {
      setEmailLoading(false);
    }
  };

  const nothingAvailable = !embyAvailable && !emailAvailable;

  // ---- Forms ----
  const embyForm = (
    <div className="space-y-5">
      <form onSubmit={submitEmby} className="space-y-4">
        <div className="space-y-2">
          <Label className={AUTH_LABEL}>{t("auth.forgotPassword.embyUsername")}</Label>
          <AuthTextInput
            value={embyUsername}
            onChange={(e) => setEmbyUsername(e.target.value)}
            autoComplete="username"
          />
        </div>
        <div className="space-y-2">
          <Label className={AUTH_LABEL}>{t("auth.forgotPassword.embyPassword")}</Label>
          <AuthPasswordField
            value={embyPassword}
            onChange={(e) => setEmbyPassword(e.target.value)}
            autoComplete="current-password"
          />
        </div>
        <AuthPrimaryButton type="submit" disabled={embyLoading}>
          {embyLoading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
          {t("auth.forgotPassword.submit")}
        </AuthPrimaryButton>
      </form>
      {embyResult && (
        <div className={AUTH_STATUS_CARD}>
          <p className="text-sm font-semibold text-[#2d2758]">
            {t("auth.forgotPassword.webUsername", { username: embyResult.username })}
          </p>
          <p className={`mt-2 ${AUTH_HINT_TEXT}`}>
            {t("auth.forgotPassword.copyHint")}
          </p>
          <div className="mt-3 flex items-center gap-2">
            <code className="min-w-0 flex-1 break-all rounded-2xl border border-white/30 bg-white/14 px-3 py-2 text-sm text-[#2d2758] shadow-[0_8px_22px_rgba(120,130,255,0.12)]">
              {embyResult.new_password}
            </code>
            <AuthIconButton
              type="button"
              onClick={() => {
                navigator.clipboard.writeText(embyResult.new_password);
                toast({ title: t("auth.forgotPassword.copied") });
              }}
              aria-label={t("auth.forgotPassword.copyPassword")}
            >
              <Copy className="h-4 w-4" />
            </AuthIconButton>
          </div>
        </div>
      )}
    </div>
  );

  const emailForm = emailDone ? (
    <div className={`${AUTH_STATUS_CARD} text-center text-sm`}>
      <p className="font-semibold text-[#3e9f76]">{t("email.forgot.resetSuccess")}</p>
      <Link href="/login" className="mt-3 inline-block font-medium text-[#3e9f76] hover:underline">
        {t("auth.forgotPassword.backToLogin")}
      </Link>
    </div>
  ) : emailStage === "request" ? (
    <form onSubmit={requestEmailCode} className="space-y-4">
      <p className={AUTH_SUPPORT_TEXT}>{t("email.forgot.description")}</p>
      <div className="space-y-2">
        <Label className={AUTH_LABEL}>{t("email.emailLabel")}</Label>
        <AuthTextInput
          type="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder={t("email.emailPlaceholder")}
          autoComplete="email"
        />
      </div>
      <AuthPrimaryButton
        type="submit"
        disabled={emailLoading || cooldown > 0}
      >
        {emailLoading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
        {cooldown > 0 ? t("email.resendIn", { seconds: cooldown }) : t("email.sendCode")}
      </AuthPrimaryButton>
    </form>
  ) : (
    <form onSubmit={submitEmailReset} className="space-y-4">
      <p className={AUTH_SUPPORT_TEXT}>{t("email.codeSentTo", { email })}</p>
      <div className="space-y-2">
        <Label className={AUTH_LABEL}>{t("email.codeLabel")}</Label>
        <AuthTextInput
          value={code}
          onChange={(e) => setCode(e.target.value)}
          placeholder={t("email.codePlaceholder")}
          inputMode="numeric"
          autoComplete="one-time-code"
        />
      </div>
      <div className="space-y-2">
        <Label className={AUTH_LABEL}>{t("email.newPassword")}</Label>
        <AuthPasswordField
          value={newPassword}
          onChange={(e) => setNewPassword(e.target.value)}
          placeholder={t("email.newPasswordPlaceholder")}
          autoComplete="new-password"
        />
      </div>
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
        <AuthPrimaryButton type="submit" className="flex-1" disabled={emailLoading}>
          {emailLoading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
          {t("email.forgot.submitReset")}
        </AuthPrimaryButton>
        <AuthSecondaryButton
          type="button"
          disabled={cooldown > 0 || emailLoading}
          onClick={() => requestEmailCode()}
        >
          {cooldown > 0 ? t("email.resendIn", { seconds: cooldown }) : t("email.resend")}
        </AuthSecondaryButton>
      </div>
    </form>
  );

  const showTabs = emailAvailable && embyAvailable;
  const showEmbyOnly = embyAvailable && !emailAvailable;
  const showEmailOnly = !embyAvailable && emailAvailable;

  return (
    <>
      <AuthBrand subtitle={t("auth.forgotPassword.description")} />

      {!forgotPasswordEnabled || nothingAvailable ? (
        <div className={`${AUTH_NOTICE_CARD} text-center`}>
          <p className="font-semibold">{t("auth.forgotPassword.adminDisabled")}</p>
        </div>
      ) : showTabs ? (
        <Tabs defaultValue="email">
          <TabsList className={`grid w-full grid-cols-2 ${AUTH_TAB_LIST}`}>
            <TabsTrigger value="email" className={AUTH_TAB_TRIGGER}>
              <Mail className="mr-1.5 h-4 w-4" />
              {t("email.forgot.emailTab")}
            </TabsTrigger>
            <TabsTrigger value="emby" className={AUTH_TAB_TRIGGER}>{t("email.forgot.embyTab")}</TabsTrigger>
          </TabsList>
          <TabsContent value="email" className="mt-4">
            {emailForm}
          </TabsContent>
          <TabsContent value="emby" className="mt-4">
            {embyForm}
          </TabsContent>
        </Tabs>
      ) : showEmbyOnly ? (
        embyForm
      ) : showEmailOnly ? (
        emailForm
      ) : null}

      <div className={`text-center text-sm ${AUTH_SUPPORT_TEXT}`}>
        <Link href="/login" className={AUTH_GHOST_LINK}>
          {t("auth.forgotPassword.backToLogin")}
        </Link>
      </div>
    </>
  );
}
