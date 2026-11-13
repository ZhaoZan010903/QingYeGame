import { request } from './api';
import type { AvatarLibraryItem, MemberProfile } from '../../../共享/profile-types';
export const profileApi = {
  async profile(): Promise<MemberProfile> {
    return (await request('/me/profile')).data;
  },
  async requestNickname(nickname: string): Promise<void> {
    await request('/me', 'PATCH', { nickname });
  },
  async avatars(): Promise<AvatarLibraryItem[]> {
    return (await request('/avatar-library')).data;
  },
};
