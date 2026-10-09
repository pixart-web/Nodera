import type { Config } from "tailwindcss";

const config: Config = {
  content: ["./app/**/*.{ts,tsx}", "./components/**/*.{ts,tsx}", "./lib/**/*.{ts,tsx}", "./mock/**/*.{ts,tsx}"],
  darkMode: "class",
  theme: {
    extend: {
      colors: {
        // Legacy scale (existing API-backed pages) remapped onto the navy tokens
        base: {
          950: "rgb(var(--color-bg) / <alpha-value>)",
          900: "rgb(var(--color-surface) / <alpha-value>)",
          800: "rgb(var(--color-surface-hover) / <alpha-value>)",
          700: "rgb(var(--color-border) / <alpha-value>)",
          600: "rgb(var(--color-border-strong) / <alpha-value>)",
          500: "#3a4a63",
          400: "rgb(var(--color-text-faint) / <alpha-value>)",
          300: "rgb(var(--color-text-muted) / <alpha-value>)",
          200: "#b9c4d6",
          100: "rgb(var(--color-text) / <alpha-value>)",
        },
        accent: { 600: "#2563eb", 500: "#3b82f6", 400: "#60a5fa" },
        danger: "rgb(var(--color-danger) / <alpha-value>)",
        warn: "rgb(var(--color-warning) / <alpha-value>)",
        ok: "rgb(var(--color-success) / <alpha-value>)",
        // Nodera design-system tokens
        nd: {
          bg: "rgb(var(--color-bg) / <alpha-value>)",
          elevated: "rgb(var(--color-bg-elevated) / <alpha-value>)",
          surface: "rgb(var(--color-surface) / <alpha-value>)",
          hover: "rgb(var(--color-surface-hover) / <alpha-value>)",
          raised: "rgb(var(--color-surface-raised) / <alpha-value>)",
          border: "rgb(var(--color-border) / <alpha-value>)",
          strong: "rgb(var(--color-border-strong) / <alpha-value>)",
          text: "rgb(var(--color-text) / <alpha-value>)",
          muted: "rgb(var(--color-text-muted) / <alpha-value>)",
          faint: "rgb(var(--color-text-faint) / <alpha-value>)",
          primary: "rgb(var(--color-primary) / <alpha-value>)",
          "primary-soft": "rgb(var(--color-primary-soft) / <alpha-value>)",
          secondary: "rgb(var(--color-secondary) / <alpha-value>)",
          success: "rgb(var(--color-success) / <alpha-value>)",
          warning: "rgb(var(--color-warning) / <alpha-value>)",
          danger: "rgb(var(--color-danger) / <alpha-value>)",
          info: "rgb(var(--color-info) / <alpha-value>)",
        },
      },
      borderRadius: { nd: "var(--radius-md)", "nd-lg": "var(--radius-lg)" },
      boxShadow: { card: "var(--shadow-card)", pop: "var(--shadow-pop)" },
      fontFamily: {
        sans: ["var(--font-inter)", "Inter", "ui-sans-serif", "system-ui", "sans-serif"],
        mono: ["ui-monospace", "SFMono-Regular", "Menlo", "monospace"],
      },
    },
  },
  plugins: [],
};

export default config;
