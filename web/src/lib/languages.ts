export const languages: [string, string][] = [
  ["en", "English"], ["fa", "Persian"], ["ar", "Arabic"], ["de", "German"],
  ["fr", "French"], ["es", "Spanish"], ["it", "Italian"], ["pt", "Portuguese"],
  ["nl", "Dutch"], ["pl", "Polish"], ["sv", "Swedish"], ["da", "Danish"],
  ["no", "Norwegian"], ["fi", "Finnish"], ["cs", "Czech"], ["hu", "Hungarian"],
  ["ro", "Romanian"], ["el", "Greek"], ["tr", "Turkish"], ["ru", "Russian"],
  ["uk", "Ukrainian"], ["he", "Hebrew"], ["hi", "Hindi"], ["ja", "Japanese"],
  ["ko", "Korean"], ["zh", "Chinese"], ["th", "Thai"], ["vi", "Vietnamese"],
  ["id", "Indonesian"],
]

export function languageName(code: string) {
  return languages.find(([id]) => id === code)?.[1] ?? code
}
