import {
  ProductImageUploader,
  type ProductImageUploaderProps,
} from '../../../共享/ProductImageUploader';
import { api, uploadConfig, createRequestId, isDemo } from '../lib/api';
type Props = Omit<
  ProductImageUploaderProps,
  'upload' | 'cancelUpload' | 'uploadConfig' | 'createRequestId' | 'isDemo' | 'transport'
> & {
  transport?: ProductImageUploaderProps['transport'];
};
export function ImageUploader(props: Props) {
  return (
    <ProductImageUploader
      {...props}
      transport={props.transport ?? api}
      upload={api.uploadProduct}
      cancelUpload={api.cancelUpload}
      uploadConfig={uploadConfig}
      createRequestId={createRequestId}
      isDemo={isDemo}
    />
  );
}
