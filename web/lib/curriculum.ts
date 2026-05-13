// The locked curriculum from the plan. SessionNav and the landing page both
// read from this single source of truth. Slugs match docs/{zh,en}/<slug>.md.
//
// "available: false" means the chapter exists in the curriculum but its
// docs aren't written yet — the link will render but go to a placeholder.

export type ChapterMeta = {
  slug: string;
  num: string; // "s01", "s02", "s_full"
  title: { zh: string; en: string };
  available: boolean;
};

export const CURRICULUM: ChapterMeta[] = [
  {
    slug: "s01-min-loop",
    num: "s01",
    title: {
      zh: "最小回路：JSON-RPC 与 stdio 帧",
      en: "Minimum loop: JSON-RPC + stdio framing",
    },
    available: true,
  },
  {
    slug: "s02-initialize",
    num: "s02",
    title: {
      zh: "初始化握手与能力协商",
      en: "Initialize handshake & capabilities",
    },
    available: true,
  },
  {
    slug: "s03-tools",
    num: "s03",
    title: {
      zh: "tools/list 与 tools/call",
      en: "tools/list and tools/call",
    },
    available: true,
  },
  {
    slug: "s04-resources",
    num: "s04",
    title: {
      zh: "资源读取、模板与订阅",
      en: "Resources: read, templates, subscribe",
    },
    available: true,
  },
  {
    slug: "s05-prompts",
    num: "s05",
    title: {
      zh: "提示模板与参数补全",
      en: "Prompts and completion/complete",
    },
    available: true,
  },
  {
    slug: "s06-sampling",
    num: "s06",
    title: {
      zh: "反向 LLM 请求：sampling",
      en: "Reverse-direction LLM: sampling",
    },
    available: false,
  },
  {
    slug: "s07-roots-elicitation",
    num: "s07",
    title: {
      zh: "根目录与表单/URL 引导",
      en: "Roots and elicitation",
    },
    available: false,
  },
  {
    slug: "s08-streamable-http",
    num: "s08",
    title: {
      zh: "Streamable HTTP 传输",
      en: "Streamable HTTP transport",
    },
    available: false,
  },
  {
    slug: "s_full-integration",
    num: "s_full",
    title: {
      zh: "端到端集成穿刺",
      en: "End-to-end integration trace",
    },
    available: false,
  },
  {
    slug: "appendix-a-design-rationale",
    num: "A",
    title: {
      zh: "附录 A · 为何 JSON-RPC + 日期版本",
      en: "Appendix A · Why JSON-RPC & date-based versioning",
    },
    available: false,
  },
  {
    slug: "appendix-b-upstream-map",
    num: "B",
    title: {
      zh: "附录 B · 上游源码导读地图",
      en: "Appendix B · Upstream source-reading map",
    },
    available: false,
  },
];

export type Locale = "zh" | "en";

export function chapterTitle(c: ChapterMeta, locale: Locale): string {
  return c.title[locale];
}
