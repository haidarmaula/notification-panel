import type { components as Staff } from './generated/staff';
import type { components as Applications } from './generated/applications';
export type Campaign = Staff['schemas']['Campaign'];
export type CampaignSummary = Staff['schemas']['CampaignSummary'];
export type CampaignInput = Staff['schemas']['CampaignInput'];
export type Page<T> = { items: T[]; next_after_id: string | null };
export type Application = Applications['schemas']['Application'];
