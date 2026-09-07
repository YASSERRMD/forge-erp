import { createContext, useContext, useState, type ReactNode } from 'react';

// Minimal EN/FR dictionary. Pattern for new strings: add the key to both
// locales here, then use t('key') — pages not yet converted stay in English
// (all keys below fall back to English when missing in French).
const dict = {
  en: {
    dashboard: 'Dashboard',
    commerce: 'Commerce',
    operations: 'Operations',
    finance: 'Finance',
    organizations: 'Organizations',
    products: 'Products',
    sales: 'Sales',
    invoices: 'Invoices',
    pos: 'Point of sale',
    manufacturing: 'Manufacturing',
    services: 'Services',
    hr: 'HR',
    booking: 'Booking',
    documents: 'Documents',
    agenda: 'Agenda',
    surveys: 'Surveys',
    members: 'Members',
    events: 'Events & hiring',
    knowledge: 'Knowledge',
    financeNav: 'Finance',
    reports: 'Reports',
    suppliers: 'Suppliers',
    payments: 'Payments',
    collections: 'Collections',
    admin: 'Administration',
    signOut: 'Sign out',
    welcome: 'Welcome',
    subtitle: 'Live operating picture across sales, finance and services',
    revenue: 'Revenue (posted)',
    receivables: 'Outstanding receivables',
    net: 'Net result',
    openTickets: 'Open tickets',
    activeProjects: 'Active projects',
    upcomingEvents: 'Upcoming events',
    revenueByMonth: 'Revenue by month (invoiced gross)',
    pnlMix: 'Profit & loss mix',
    needsAttention: 'Needs attention (open tickets)',
    queueClear: 'Queue clear.',
    upcoming: 'Upcoming events',
    noneScheduled: 'Nothing scheduled.',
    liveNote: 'Figures stream live from /reports endpoints — no mocks.',
    create: 'Create',
    save: 'Save',
    cancel: 'Cancel',
    close: 'Close',
    validate: 'Validate',
    activate: 'Activate',
    checkout: 'Checkout',
    pay: 'Pay',
    produce: 'Produce',
    publish: 'Publish',
    open: 'Open',
    resolve: 'Resolve',
    reopen: 'Reopen',
    approve: 'Approve',
    submit: 'Submit',
    search: 'Search',
  },
  fr: {
    dashboard: 'Tableau de bord',
    commerce: 'Commerce',
    operations: 'Opérations',
    finance: 'Finance',
    organizations: 'Tiers',
    products: 'Produits',
    sales: 'Ventes',
    invoices: 'Factures',
    pos: 'Caisse',
    manufacturing: 'Fabrication',
    services: 'Services',
    hr: 'RH',
    booking: 'Réservations',
    documents: 'Documents',
    agenda: 'Agenda',
    surveys: 'Sondages',
    members: 'Adhérents',
    events: 'Événements & recrutement',
    knowledge: 'Connaissances',
    financeNav: 'Finance',
    reports: 'Rapports',
    suppliers: 'Fournisseurs',
    payments: 'Paiements',
    collections: 'Recouvrements',
    admin: 'Administration',
    signOut: 'Déconnexion',
    welcome: 'Bienvenue',
    subtitle: 'Vue temps réel des ventes, de la finance et des services',
    revenue: 'Chiffre d’affaires (comptabilisé)',
    receivables: 'Créances clients',
    net: 'Résultat net',
    openTickets: 'Tickets ouverts',
    activeProjects: 'Projets actifs',
    upcomingEvents: 'Événements à venir',
    revenueByMonth: 'Chiffre d’affaires par mois (facturé TTC)',
    pnlMix: 'Résultat : composition',
    needsAttention: 'À traiter (tickets ouverts)',
    queueClear: 'File vide.',
    upcoming: 'Événements à venir',
    noneScheduled: 'Rien de prévu.',
    liveNote: 'Chiffres en direct des endpoints /reports — sans mocks.',
    create: 'Créer',
    save: 'Enregistrer',
    cancel: 'Annuler',
    close: 'Clôturer',
    validate: 'Valider',
    activate: 'Activer',
    checkout: 'Encaisser',
    pay: 'Payer',
    produce: 'Produire',
    publish: 'Publier',
    open: 'Ouvrir',
    resolve: 'Résoudre',
    reopen: 'Rouvrir',
    approve: 'Approuver',
    submit: 'Soumettre',
    search: 'Rechercher',
  },
} as const;

export type Lang = keyof typeof dict;
export type DictKey = keyof (typeof dict)['en'];

interface LangState {
  lang: Lang;
  setLang: (l: Lang) => void;
  t: (k: DictKey) => string;
}

const LangContext = createContext<LangState>({
  lang: 'en',
  setLang: () => undefined,
  t: (k) => k,
});

export function LangProvider({ children }: { children: ReactNode }) {
  const [lang, setLang] = useState<Lang>(() => {
    const saved = localStorage.getItem('ferp_lang');
    return saved === 'fr' ? 'fr' : 'en';
  });
  const switchLang = (l: Lang) => {
    setLang(l);
    localStorage.setItem('ferp_lang', l);
  };
  const t = (k: DictKey): string => dict[lang][k] ?? dict.en[k] ?? k;
  return <LangContext.Provider value={{ lang, setLang: switchLang, t }}>{children}</LangContext.Provider>;
}

export function useLang(): LangState {
  return useContext(LangContext);
}
