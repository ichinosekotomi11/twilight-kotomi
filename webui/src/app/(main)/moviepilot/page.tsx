"use client";

import { useRef, useState } from "react";
import { Download, Loader2, Search } from "lucide-react";
import { api } from "@/lib/api";
import { useAuthStore } from "@/store/auth";
import { useToast } from "@/hooks/use-toast";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";

type MoviePilotResult = {
  title?: string;
  year?: string | number;
  type?: string;
  resource_pix?: string;
  video_encode?: string;
  resource_team?: string;
  seeders?: number;
  size?: string | number;
  description?: string;
  tmdbid?: string | number;
  doubanid?: string | number;
  torrent_info?: Record<string, unknown>;
  meta_info?: Record<string, unknown>;
};

function readString(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function readNumber(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function readStringOrNumber(value: unknown): string | number | undefined {
  if (typeof value === "string" || typeof value === "number") return value;
  return undefined;
}

function formatBytes(value: string | number | undefined): string {
  if (typeof value === "string" && value.trim()) return value.trim();
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0) return "";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let size = value;
  let index = 0;
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index += 1;
  }
  const digits = size >= 100 || index === 0 ? 0 : size >= 10 ? 1 : 2;
  return `${size.toFixed(digits)} ${units[index]}`;
}

function formatFreeLabel(torrent: Record<string, unknown>): string {
  const factor = readString(torrent.volume_factor);
  const labels = Array.isArray(torrent.labels) ? torrent.labels.map(readString).filter(Boolean) : [];
  const downloadFactor = readNumber(torrent.downloadvolumefactor);
  if (downloadFactor === 0) return "免费";
  if (labels.some((label) => /免费|free|2xfree/i.test(label))) return labels.find((label) => /免费|free|2xfree/i.test(label)) || "免费";
  if (/免费|free/i.test(factor)) return factor;
  if (factor) return factor;
  if (readString(torrent.freedate)) return "限免";
  return "普通";
}

function compactTags(item: MoviePilotResult): string[] {
  const torrent = item.torrent_info ?? {};
  const siteName = readString(torrent.site_name);
  return [
    item.resource_pix,
    item.video_encode,
    formatFreeLabel(torrent),
    formatBytes(item.size),
    item.resource_team,
    siteName,
  ].filter(Boolean) as string[];
}

function normalizeResult(value: unknown): MoviePilotResult | null {
  if (!value || typeof value !== "object") return null;
  const raw = value as Record<string, unknown>;
  const meta = raw.meta_info && typeof raw.meta_info === "object" ? (raw.meta_info as Record<string, unknown>) : {};
  const torrent = raw.torrent_info && typeof raw.torrent_info === "object" ? (raw.torrent_info as Record<string, unknown>) : {};
  const media = raw.media_info && typeof raw.media_info === "object" ? (raw.media_info as Record<string, unknown>) : {};
  const title = readString(raw.title) || readString(meta.title) || readString(meta.name) || readString(torrent.title);
  const description = readString(raw.description) || readString(meta.subtitle) || readString(torrent.description);
  const year = readStringOrNumber(raw.year) ?? readStringOrNumber(meta.year);
  const size = readStringOrNumber(raw.size) ?? readStringOrNumber(torrent.size);
  const seeders = readNumber(raw.seeders) ?? readNumber(torrent.seeders);

  return {
    ...raw,
    title,
    description,
    year,
    size,
    seeders,
    type: readString(raw.type) || readString(meta.type),
    resource_pix: readString(raw.resource_pix) || readString(meta.resource_pix),
    video_encode: readString(raw.video_encode) || readString(meta.video_encode),
    resource_team: readString(raw.resource_team) || readString(meta.resource_team),
    tmdbid: readStringOrNumber(raw.tmdbid) ?? readStringOrNumber(meta.tmdbid) ?? readStringOrNumber(media.tmdb_id) ?? readStringOrNumber(media.tmdbid),
    doubanid: readStringOrNumber(raw.doubanid) ?? readStringOrNumber(meta.doubanid) ?? readStringOrNumber(media.douban_id) ?? readStringOrNumber(media.doubanid),
    torrent_info: torrent,
    meta_info: meta,
  };
}

function looksLikeMoviePilotResult(value: unknown): boolean {
  if (!value || typeof value !== "object") return false;
  const raw = value as Record<string, unknown>;
  const meta = raw.meta_info && typeof raw.meta_info === "object" ? (raw.meta_info as Record<string, unknown>) : {};
  const torrent = raw.torrent_info && typeof raw.torrent_info === "object" ? (raw.torrent_info as Record<string, unknown>) : {};
  return Boolean(
    raw.torrent_info ||
    raw.media_info ||
    raw.title ||
    raw.description ||
    raw.resource_pix ||
    raw.video_encode ||
    meta.title ||
    meta.name ||
    torrent.title
  );
}

function collectResultItems(value: unknown, depth = 0): MoviePilotResult[] {
  if (depth > 6 || value == null) return [];
  if (Array.isArray(value)) {
    return value.map(normalizeResult).filter((item): item is MoviePilotResult => item !== null);
  }
  if (typeof value !== "object") return [];
  if (looksLikeMoviePilotResult(value)) {
    const item = normalizeResult(value);
    return item ? [item] : [];
  }
  const raw = value as Record<string, unknown>;
  for (const key of ["data", "items", "results", "list", "resources", "torrents"]) {
    const nested = collectResultItems(raw[key], depth + 1);
    if (nested.length > 0) return nested;
  }
  for (const nestedValue of Object.values(raw)) {
    const nested = collectResultItems(nestedValue, depth + 1);
    if (nested.length > 0) return nested;
  }
  return [];
}

function resultItems(value: unknown): MoviePilotResult[] {
  return collectResultItems(value);
}

export default function MoviePilotPage() {
  const { user } = useAuthStore();
  const { toast } = useToast();
  const inputRef = useRef<HTMLInputElement | null>(null);
  const searchSeqRef = useRef(0);
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(false);
  const [submitting, setSubmitting] = useState<string | null>(null);
  const [results, setResults] = useState<MoviePilotResult[]>([]);
  const allowed = user?.role === 0 || user?.role === 2;

  const search = async (rawQuery?: string) => {
    const keyword = (rawQuery ?? inputRef.current?.value ?? query).trim();
    if (!keyword || loading) return;
    const searchSeq = searchSeqRef.current + 1;
    searchSeqRef.current = searchSeq;
    setQuery(keyword);
    setResults([]);
    setLoading(true);
    try {
      const response = await api.searchMoviePilot(keyword);
      if (!response.success) throw new Error(response.message);
      if (searchSeq === searchSeqRef.current) {
        setResults(resultItems(response.data));
      }
    } catch (error) {
      if (searchSeq === searchSeqRef.current) {
        toast({ title: "MoviePilot 搜索失败", description: error instanceof Error ? error.message : "网络错误", variant: "destructive" });
      }
    } finally {
      if (searchSeq === searchSeqRef.current) {
        setLoading(false);
      }
    }
  };

  const download = async (item: MoviePilotResult) => {
    if (!item.torrent_info) return;
    const key = `${item.title || "resource"}-${item.size || ""}`;
    setSubmitting(key);
    try {
      const requestPayload: Record<string, unknown> = { torrent_in: item.torrent_info };
      if (item.tmdbid != null && item.tmdbid !== "") requestPayload.tmdbid = item.tmdbid;
      if (item.doubanid != null && item.doubanid !== "") requestPayload.doubanid = item.doubanid;
      const response = await api.addMoviePilotDownload(requestPayload);
      if (!response.success) throw new Error(response.message);
      const resultPayload = response.data as { points_cost?: number; remaining_points?: number } | undefined;
      const cost = Number(resultPayload?.points_cost || 0);
      const remaining = Number(resultPayload?.remaining_points || 0);
      toast({
        title: "下载任务已提交",
        description: cost > 0 ? `${item.title || "MoviePilot"}，已扣除 ${cost} 小兔，剩余 ${remaining}` : (item.title || "MoviePilot"),
        variant: "success",
      });
    } catch (error) {
      toast({ title: "提交失败", description: error instanceof Error ? error.message : "网络错误", variant: "destructive" });
    } finally {
      setSubmitting(null);
    }
  };

  if (!allowed) {
    return <p className="py-16 text-center text-sm text-muted-foreground">MP 自助下载中心仅对白名单开放。</p>;
  }

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-2xl font-semibold">MP 自助下载中心</h1>
        <p className="mt-1 text-sm text-muted-foreground">连接远端 MoviePilot v2，白名单专属媒体搜索与下载。</p>
      </div>
      <div className="flex gap-2">
        <Input
          ref={inputRef}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              void search(event.currentTarget.value);
            }
          }}
          placeholder="搜索电影、剧集或资源"
        />
        <Button onClick={() => void search()} disabled={loading || !query.trim()}>
          {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Search className="h-4 w-4" />}
          <span className="ml-2">搜索</span>
        </Button>
      </div>
      <div className="grid gap-3">
        {results.map((item, index) => {
          const key = `${item.title || index}-${item.size || ""}`;
          const torrent = item.torrent_info ?? {};
          const siteName = readString(torrent.site_name);
          const siteCategory = readString(torrent.category);
          const tags = compactTags(item);
          return (
            <Card key={key}>
              <CardContent className="flex items-start justify-between gap-4 p-4">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <h2 className="min-w-0 text-base font-medium leading-6">
                      {item.title || "未命名资源"} {item.year ? `(${item.year})` : ""}
                    </h2>
                    {siteName ? <Badge variant="outline" className="rounded-md text-[11px]">{siteName}</Badge> : null}
                    {item.seeders != null ? <Badge variant="secondary" className="rounded-md text-[11px]">{item.seeders} 做种</Badge> : null}
                  </div>
                  {item.description ? (
                    <p className="mt-2 line-clamp-2 text-sm text-muted-foreground">{item.description}</p>
                  ) : null}
                  <div className="mt-3 flex flex-wrap gap-2">
                    {tags.map((tag) => (
                      <Badge key={`${key}-${tag}`} variant="secondary" className="rounded-md px-2 py-1 text-[11px] font-medium">
                        {tag}
                      </Badge>
                    ))}
                    {siteCategory ? (
                      <Badge variant="outline" className="rounded-md px-2 py-1 text-[11px]">
                        {siteCategory}
                      </Badge>
                    ) : null}
                  </div>
                </div>
                <Button size="icon" title="提交下载" className="shrink-0" onClick={() => void download(item)} disabled={!item.torrent_info || submitting === key}>
                  {submitting === key ? <Loader2 className="h-4 w-4 animate-spin" /> : <Download className="h-4 w-4" />}
                </Button>
              </CardContent>
            </Card>
          );
        })}
      </div>
    </div>
  );
}
