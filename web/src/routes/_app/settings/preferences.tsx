import { createFileRoute } from '@tanstack/react-router';
import { PreferencesForm } from '@/features/settings/preferences-form';

export const Route = createFileRoute('/_app/settings/preferences')({ component: PreferencesForm });
