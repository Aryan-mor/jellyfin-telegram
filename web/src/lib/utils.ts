import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function bytes(n: number) {
  if (!n) return "0 MB"
  if (n >= 1e9) return (n / 1e9).toFixed(1) + " GB"
  return Math.round(n / 1e6) + " MB"
}
