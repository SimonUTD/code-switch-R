export type AutomationCard = {
  id: number
  name: string
  apiUrl: string
  apiKey: string
  officialSite: string
  icon: string
  tint: string
  accent: string
  enabled: boolean
}

export const automationCardGroups: Record<'claude' | 'codex', AutomationCard[]> = {
  claude: [
    {
      id: 101,
      name: 'AICoding.sh',
      apiUrl: 'https://api.aicoding.sh',
      apiKey: '',
      officialSite: 'https://aicoding.sh',
      icon: 'aicoding',
      tint: 'rgba(10, 132, 255, 0.14)',
      accent: '#0a84ff',
      enabled: false,
    },
    {
      id: 102,
      name: 'Kimi',
      apiUrl: 'https://api.moonshot.cn/anthropic',
      apiKey: '',
      officialSite: 'https://kimi.moonshot.cn',
      icon: 'kimi',
      tint: 'rgba(16, 185, 129, 0.16)',
      accent: '#10b981',
      enabled: false,
    },
    {
      id: 103,
      name: 'Deepseek',
      apiUrl: 'https://api.deepseek.com/anthropic',
      apiKey: '',
      officialSite: 'https://www.deepseek.com',
      icon: 'deepseek',
      tint: 'rgba(251, 146, 60, 0.18)',
      accent: '#f97316',
      enabled: false,
    },
  ],
  codex: [
    {
      id: 201,
      name: 'AICoding.sh',
      apiUrl: 'https://api.aicoding.sh',
      apiKey: '',
      officialSite: 'https://www.aicoding.sh',
      icon: 'aicoding',
      tint: 'rgba(236, 72, 153, 0.16)',
      accent: '#ec4899',
      enabled: false,
    },
  ],
}

export function createAutomationCards(data: AutomationCard[] = []): AutomationCard[] {
  return data.map((item) => ({
    ...item,
    officialSite: item.officialSite ?? '',
  }))
}
