// English-only i18n. The translation map is kept so the t(key) call sites
// don't have to change, but there is no language switching anymore.
const translations = {};
const currentLanguage = 'en';

export function setLanguage(_lang) {
    // No-op: this build is English-only.
}

export function getLanguage() {
    return currentLanguage;
}

export function t(key) {
    const keys = key.split('.');
    let value = translations[currentLanguage];

    for (const k of keys) {
        if (value && typeof value === 'object') {
            value = value[k];
        } else {
            return key;
        }
    }

    return value || key;
}

export function loadTranslations(langStrings) {
    Object.assign(translations, langStrings);
}

export function initLanguage() {
    // No persistence, no detection — always English.
}
