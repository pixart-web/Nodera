import type { Config } from "tailwindcss";

const config: Config = {
  content: ["./app/**/*.{ts,tsx}", "./components/**/*.{ts,tsx}", "./lib/**/*.{ts,tsx}"],
  darkMode: "class",
  theme: {
    extend: {
      colors: {
        base: {
          950: "#08090c",
          900: "#0d0f14",
          800: "#12151c",
          700: "#1b1f29",
          600: "#262b38",
          500: "#3a4152",
          400: "#5b6478",
          300: "#8a93a8",
          200: "#b9c0d0",
          100: "#dde1ea",
        },
        accent: {
          600: "#0891b2",
          500: "#06b6d4",
          400: "#22d3ee",
        },
        danger: "#f87171",
        warn: "#fbbf24",
        ok: "#34d399",
      },
      fontFamily: {
        mono: ["ui-monospace", "SFMono-Regular", "Menlo", "monospace"],
      },
    },
  },
  plugins: [],
};

export default config;
